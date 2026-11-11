// Package mail sends HR's own notices over SMTP.
package mail

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/AGlElectronics/hr/backend/internal/denlink"
)

// Message is a plain-text email.
type Message struct {
	To      string
	Subject string
	Body    string
}

// Sender delivers messages. Nil-safe callers check Enabled first.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// SMTPConfig is where and as whom to send.
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	FromName string
	// Security is "" (STARTTLS when the server offers it), "starttls"
	// (required), "tls" (implicit TLS, usually port 465) or "none".
	Security string
}

// passwordSource fetches the SMTP password Den keeps out of the bundle.
// *denlink.Client implements it (SMTPPassword).
type passwordSource interface {
	SMTPPassword(ctx context.Context) (string, error)
}

// FromDen turns Den's SMTP module setting into a config. set is false when
// Den sets no smtp.host: ok is false and the caller keeps its own SMTP_*
// settings, including SMTP_PASSWORD. When Den sets a server and
// password_set, the password comes from den.SMTPPassword and the
// environment password is not used. An error from that call is returned
// so the send does not fall back to SMTP_PASSWORD. When password_set is
// false the config has no password. An empty password with a nil error
// means Den holds none.
func FromDen(ctx context.Context, den passwordSource, s denlink.SMTP, set bool) (SMTPConfig, bool, error) {
	if !set || strings.TrimSpace(s.Host) == "" {
		return SMTPConfig{}, false, nil
	}
	c := SMTPConfig{Host: s.Host, Port: s.Port, Username: s.Username, From: s.From, FromName: s.FromName, Security: s.Security}
	if !s.PasswordSet {
		return c, true, nil
	}
	if den == nil {
		return SMTPConfig{}, false, denlink.ErrNotPaired
	}
	pw, err := den.SMTPPassword(ctx)
	if err != nil {
		return SMTPConfig{}, false, err
	}
	c.Password = pw
	return c, true, nil
}

// Dynamic picks the server for each message. Pick (Den's settings while
// paired) wins when it returns ok. Fallback (SMTP_*, may be nil) is used
// only when Den sets no server. An error from Pick is returned as-is, so
// a Den server whose password cannot be fetched does not send with
// SMTP_PASSWORD instead.
type Dynamic struct {
	Pick     func(ctx context.Context) (SMTPConfig, bool, error)
	Fallback *SMTP
}

// Send delivers m through the server picked now.
func (d *Dynamic) Send(ctx context.Context, m Message) error {
	if d.Pick != nil {
		cfg, ok, err := d.Pick(ctx)
		if err != nil {
			return err
		}
		if ok {
			return NewSMTP(cfg).Send(ctx, m)
		}
	}
	return d.Fallback.Send(ctx, m)
}

// SMTP sends through a plain or STARTTLS SMTP server (Mailpit in dev).
type SMTP struct{ cfg SMTPConfig }

// NewSMTP returns nil when no host is configured.
func NewSMTP(cfg SMTPConfig) *SMTP {
	if strings.TrimSpace(cfg.Host) == "" {
		return nil
	}
	if cfg.Port == 0 {
		cfg.Port = 587
	}
	return &SMTP{cfg: cfg}
}

// ErrNotConfigured is returned when sending without an SMTP server.
var ErrNotConfigured = errors.New("email is not configured (set SMTP_HOST)")

// Send delivers m. It honours ctx for the dial only.
func (s *SMTP) Send(ctx context.Context, m Message) error {
	if s == nil {
		return ErrNotConfigured
	}
	if s.cfg.From == "" {
		return errors.New("SMTP_FROM is not set")
	}
	addr := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))
	d := net.Dialer{Timeout: 10 * time.Second}
	tlsCfg := &tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12}
	var conn net.Conn
	var err error
	if s.cfg.Security == "tls" {
		conn, err = (&tls.Dialer{NetDialer: &d, Config: tlsCfg}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("smtp dial %s: %w", addr, err)
	}
	c, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("smtp hello: %w", err)
	}
	defer c.Close()
	switch ok, _ := c.Extension("STARTTLS"); {
	case s.cfg.Security == "tls" || s.cfg.Security == "none":
	case ok:
		if err := c.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("smtp starttls: %w", err)
		}
	case s.cfg.Security == "starttls":
		return errors.New("smtp: the server does not offer STARTTLS")
	}
	if s.cfg.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := c.Mail(s.cfg.From); err != nil {
		return fmt.Errorf("smtp from: %w", err)
	}
	if err := c.Rcpt(m.To); err != nil {
		return fmt.Errorf("smtp to %s: %w", m.To, err)
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(s.render(m)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

func (s *SMTP) render(m Message) []byte {
	from := s.cfg.From
	if s.cfg.FromName != "" {
		from = fmt.Sprintf("%s <%s>", mime.QEncoding.Encode("utf-8", s.cfg.FromName), s.cfg.From)
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", m.To)
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", m.Subject))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n")
	b.WriteString(strings.ReplaceAll(m.Body, "\n", "\r\n"))
	return b.Bytes()
}
