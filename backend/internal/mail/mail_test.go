package mail

import (
	"bufio"
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"testing"

	"github.com/AGlElectronics/hr/backend/internal/denlink"
)

// fakeSMTP accepts one message and returns what it received.
func fakeSMTP(t *testing.T) (port int, received <-chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	out := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		write := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }
		write("220 fake ESMTP")
		var data strings.Builder
		inData := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			if inData {
				if line == ".\r\n" {
					inData = false
					write("250 OK")
					out <- data.String()
					continue
				}
				data.WriteString(line)
				continue
			}
			cmd := strings.ToUpper(strings.TrimSpace(line))
			switch {
			case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
				write("250-fake")
				write("250 AUTH PLAIN")
			case strings.HasPrefix(cmd, "AUTH"):
				write("235 OK")
			case strings.HasPrefix(cmd, "MAIL"), strings.HasPrefix(cmd, "RCPT"):
				data.WriteString(strings.TrimSpace(line) + "\n")
				write("250 OK")
			case cmd == "DATA":
				inData = true
				write("354 go ahead")
			case cmd == "QUIT":
				write("221 bye")
				return
			default:
				write("250 OK")
			}
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, out
}

func TestSMTPSend(t *testing.T) {
	port, received := fakeSMTP(t)
	s := NewSMTP(SMTPConfig{Host: "127.0.0.1", Port: port, From: "hr@example.org", FromName: "Con HR"})
	err := s.Send(context.Background(), Message{To: "ada@example.org", Subject: "Grüße", Body: "line one\nline two"})
	if err != nil {
		t.Fatal(err)
	}
	got := <-received
	for _, want := range []string{"MAIL FROM:<hr@example.org>", "RCPT TO:<ada@example.org>", "To: ada@example.org",
		"Subject: =?utf-8?q?Gr=C3=BC=C3=9Fe?=", "line one\r\nline two", "Content-Type: text/plain; charset=utf-8"} {
		if !strings.Contains(got, want) {
			t.Errorf("message lacks %q:\n%s", want, got)
		}
	}
}

func TestSMTPNotConfigured(t *testing.T) {
	if NewSMTP(SMTPConfig{}) != nil {
		t.Fatal("no host should mean no mailer")
	}
	var s *SMTP
	if err := s.Send(context.Background(), Message{}); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("nil mailer: %v", err)
	}
	if err := NewSMTP(SMTPConfig{Host: "x"}).Send(context.Background(), Message{To: "a@b.c"}); err == nil || !strings.Contains(err.Error(), "SMTP_FROM") {
		t.Fatalf("missing from: %v", err)
	}
}

type passwordFunc func(context.Context) (string, error)

func (f passwordFunc) SMTPPassword(ctx context.Context) (string, error) { return f(ctx) }

func TestFromDen(t *testing.T) {
	ctx := context.Background()
	smtp := denlink.SMTP{
		Host: "smtp.den.example", Port: 2525, Security: "starttls",
		Username: "hr", PasswordSet: true, From: "hr@den.example", FromName: "Con HR",
	}

	if _, ok, err := FromDen(ctx, nil, denlink.SMTP{}, false); err != nil || ok {
		t.Fatalf("no server: ok %v err %v", ok, err)
	}
	if _, ok, err := FromDen(ctx, passwordFunc(func(context.Context) (string, error) {
		t.Fatal("SMTPPassword called when Den sets no server")
		return "", nil
	}), denlink.SMTP{PasswordSet: true}, false); err != nil || ok {
		t.Fatalf("unset: ok %v err %v", ok, err)
	}

	noPw := smtp
	noPw.PasswordSet = false
	cfg, ok, err := FromDen(ctx, passwordFunc(func(context.Context) (string, error) {
		t.Fatal("SMTPPassword called when password_set is false")
		return "", nil
	}), noPw, true)
	if err != nil || !ok || cfg.Password != "" || cfg.Host != smtp.Host || cfg.FromName != "Con HR" || cfg.Security != "starttls" {
		t.Fatalf("no password_set: %+v ok %v err %v", cfg, ok, err)
	}

	var gotKey string
	cfg, ok, err = FromDen(ctx, passwordFunc(func(context.Context) (string, error) {
		gotKey = "called"
		return "from-den", nil
	}), smtp, true)
	if err != nil || !ok || cfg.Password != "from-den" || gotKey != "called" || cfg.Username != "hr" {
		t.Fatalf("password from Den: %+v ok %v err %v called %q", cfg, ok, err, gotKey)
	}

	cfg, ok, err = FromDen(ctx, passwordFunc(func(context.Context) (string, error) {
		return "", nil
	}), smtp, true)
	if err != nil || !ok || cfg.Password != "" {
		t.Fatalf("Den holds none: %+v ok %v err %v", cfg, ok, err)
	}

	_, ok, err = FromDen(ctx, passwordFunc(func(context.Context) (string, error) {
		return "", denlink.ErrRevoked
	}), smtp, true)
	if !errors.Is(err, denlink.ErrRevoked) || ok {
		t.Fatalf("revoked: ok %v err %v", ok, err)
	}
	_, _, err = FromDen(ctx, nil, smtp, true)
	if !errors.Is(err, denlink.ErrNotPaired) {
		t.Fatalf("nil source: %v", err)
	}
}

func TestDynamicUsesDenOrEnv(t *testing.T) {
	port, received := fakeSMTP(t)
	envPort, envReceived := fakeSMTP(t)
	ctx := context.Background()

	denServer := denlink.SMTP{Host: "127.0.0.1", Port: port, From: "den@example.org", Username: "hr", PasswordSet: true}
	d := &Dynamic{
		Fallback: NewSMTP(SMTPConfig{Host: "127.0.0.1", Port: envPort, From: "env@example.org", Password: "env-secret"}),
		Pick: func(ctx context.Context) (SMTPConfig, bool, error) {
			return FromDen(ctx, passwordFunc(func(context.Context) (string, error) {
				return "den-secret", nil
			}), denServer, true)
		},
	}
	if err := d.Send(ctx, Message{To: "ada@example.org", Subject: "hi", Body: "from den"}); err != nil {
		t.Fatal(err)
	}
	got := <-received
	if !strings.Contains(got, "MAIL FROM:<den@example.org>") || strings.Contains(got, "env-secret") {
		t.Fatalf("sent via env or missing Den from:\n%s", got)
	}
	select {
	case extra := <-envReceived:
		t.Fatalf("fallback also sent:\n%s", extra)
	default:
	}

	d.Pick = func(ctx context.Context) (SMTPConfig, bool, error) {
		return FromDen(ctx, passwordFunc(func(context.Context) (string, error) {
			return "", denlink.ErrRevoked
		}), denServer, true)
	}
	if err := d.Send(ctx, Message{To: "ada@example.org", Subject: "hi", Body: "no"}); !errors.Is(err, denlink.ErrRevoked) {
		t.Fatalf("revoked should not fall back to SMTP_PASSWORD: %v", err)
	}

	d.Pick = func(ctx context.Context) (SMTPConfig, bool, error) {
		return FromDen(ctx, nil, denlink.SMTP{}, false)
	}
	if err := d.Send(ctx, Message{To: "ada@example.org", Subject: "hi", Body: "from env"}); err != nil {
		t.Fatal(err)
	}
	envGot := <-envReceived
	if !strings.Contains(envGot, "MAIL FROM:<env@example.org>") {
		t.Fatalf("fallback:\n%s", envGot)
	}
}

func TestSMTPDialError(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	err := NewSMTP(SMTPConfig{Host: "127.0.0.1", Port: port, From: "hr@example.org"}).Send(context.Background(), Message{To: "a@b.c"})
	if err == nil || !strings.Contains(err.Error(), "smtp dial 127.0.0.1:"+strconv.Itoa(port)) {
		t.Fatalf("dial error: %v", err)
	}
}
