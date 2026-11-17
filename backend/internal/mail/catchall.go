package mail

import (
	"context"
	"fmt"
	"strings"

	"github.com/AGlElectronics/hr/backend/internal/tgcatchall"
)

// Catchall mirrors every email to the Compose Telegram catch-all (dev only)
// and still delivers through Inner when that is configured.
type Catchall struct {
	Inner  Sender
	TG     *tgcatchall.Client
	Module string // e.g. "HR"
}

// Send posts a prefixed copy to Telegram when the catch-all is on, then
// sends through Inner. If Inner is missing but the catch-all is on, the
// Telegram copy alone counts as success (local Compose without SMTP).
func (c *Catchall) Send(ctx context.Context, m Message) error {
	if c != nil && c.TG.Enabled() {
		mod := strings.TrimSpace(c.Module)
		if mod == "" {
			mod = "email"
		}
		intended := fmt.Sprintf("%s email → %s", mod, strings.TrimSpace(m.To))
		if sub := strings.TrimSpace(m.Subject); sub != "" {
			intended += " · " + sub
		}
		_ = c.TG.Send(ctx, intended, m.Body)
		if c.Inner == nil || isNilSender(c.Inner) {
			return nil
		}
	}
	if c == nil || c.Inner == nil || isNilSender(c.Inner) {
		return ErrNotConfigured
	}
	return c.Inner.Send(ctx, m)
}

func isNilSender(s Sender) bool {
	switch v := s.(type) {
	case *SMTP:
		return v == nil
	case *Dynamic:
		return v == nil
	case *Catchall:
		return v == nil
	}
	return false
}
