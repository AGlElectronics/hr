package service

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/AGlElectronics/hr/backend/internal/mail"
	"github.com/AGlElectronics/hr/backend/internal/provisioners"
)

// AdminNotifier emails HR admins: the addresses in the settings
// (admin_notification_emails), or else the members of the HR admin groups.
type AdminNotifier struct {
	settings    *SettingsService
	mailer      mail.Sender            // nil: email not configured
	directory   provisioners.Directory // nil: no group lookup
	adminGroups []string
}

// NewAdminNotifier wires an AdminNotifier.
func NewAdminNotifier(settings *SettingsService, mailer mail.Sender, directory provisioners.Directory, adminGroups []string) *AdminNotifier {
	return &AdminNotifier{settings: settings, mailer: mailer, directory: directory, adminGroups: adminGroups}
}

// ErrNoRecipients means HR knows no admin address to write to.
var ErrNoRecipients = errors.New("no HR admin email address: set admin notification emails in HR settings")

// Recipients lists the admin addresses.
func (n *AdminNotifier) Recipients(ctx context.Context) ([]string, error) {
	st, err := n.settings.Get(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	add := func(e string) {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" && !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}
	for _, e := range strings.Split(st.AdminNotificationEmails, ",") {
		add(e)
	}
	if len(out) == 0 && n.directory != nil {
		groups, err := n.directory.ListGroups(ctx)
		if err != nil {
			return nil, err
		}
		want := map[string]bool{}
		for _, g := range n.adminGroups {
			want[strings.Trim(strings.ToLower(g), "/")] = true
		}
		for _, g := range groups {
			if want[strings.ToLower(g.Name)] || want[strings.Trim(strings.ToLower(g.Path), "/")] {
				for _, m := range g.Members {
					if m.Enabled {
						add(m.Email)
					}
				}
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// Send emails every admin; it fails when nobody could be told.
func (n *AdminNotifier) Send(ctx context.Context, subject, body string) error {
	if n == nil || n.mailer == nil {
		return mail.ErrNotConfigured
	}
	to, err := n.Recipients(ctx)
	if err != nil {
		return err
	}
	if len(to) == 0 {
		return ErrNoRecipients
	}
	var sent int
	var lastErr error
	for _, addr := range to {
		if err := n.mailer.Send(ctx, mail.Message{To: addr, Subject: subject, Body: body}); err != nil {
			lastErr = err
			continue
		}
		sent++
	}
	if sent == 0 {
		return lastErr
	}
	return nil
}
