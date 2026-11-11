package mail

import (
	"bufio"
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"testing"
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
				write("250 fake")
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

func TestSMTPDialError(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	err := NewSMTP(SMTPConfig{Host: "127.0.0.1", Port: port, From: "hr@example.org"}).Send(context.Background(), Message{To: "a@b.c"})
	if err == nil || !strings.Contains(err.Error(), "smtp dial 127.0.0.1:"+strconv.Itoa(port)) {
		t.Fatalf("dial error: %v", err)
	}
}
