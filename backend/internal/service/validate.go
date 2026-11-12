package service

import (
	"context"
	"errors"
	"net"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/nyaruka/phonenumbers"

	"github.com/AGlElectronics/hr/backend/internal/domain"
)

// Names may contain letters (any script, with accents), spaces, periods,
// hyphens and apostrophes, and must start with a letter. Digits, emoji and
// other symbols are refused.
var (
	nameRE       = regexp.MustCompile(`^\p{L}[\p{L}\p{M} .'\-]*$`)
	spaceRunRE   = regexp.MustCompile(`\s+`)
	countryRE    = regexp.MustCompile(`^[A-Z]{2}$`)
	emailLocalRE = regexp.MustCompile(`^[a-z0-9._%+\-]+$`)
	orgLocalRE   = regexp.MustCompile(`^[a-z0-9]([a-z0-9._\-]*[a-z0-9])?$`)
)

const nameRule = "may only contain letters, spaces, periods, hyphens and apostrophes"

// cleanName trims, collapses whitespace and unifies apostrophes.
func cleanName(s string) string {
	s = strings.NewReplacer("’", "'", "‘", "'", "ʼ", "'").Replace(s)
	return spaceRunRE.ReplaceAllString(strings.TrimSpace(s), " ")
}

func validName(s string) bool {
	return nameRE.MatchString(s)
}

// validEmail accepts plain ASCII addresses with a dotted domain.
func validEmail(s string) bool {
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address != s {
		return false
	}
	at := strings.LastIndex(s, "@")
	if at <= 0 || !emailLocalRE.MatchString(s[:at]) {
		return false
	}
	return domainRE.MatchString(s[at+1:])
}

// NormalizePhone parses a number (international, or national for the
// default country) and returns it in E.164 form, or "" when invalid.
func NormalizePhone(raw, defaultCountry string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	region := strings.ToUpper(defaultCountry)
	if strings.HasPrefix(raw, "00") {
		raw = "+" + raw[2:]
	}
	if region == "" && !strings.HasPrefix(raw, "+") {
		return ""
	}
	num, err := phonenumbers.Parse(raw, region)
	if err != nil || !phonenumbers.IsValidNumber(num) {
		return ""
	}
	return phonenumbers.Format(num, phonenumbers.E164)
}

func phoneRule(defaultCountry string) string {
	if defaultCountry == "" {
		return "must be a valid phone number with country code, e.g. +43 660 1234567"
	}
	return "must be a valid phone number"
}

// normalizeAddress trims the parts and uppercases the country code.
func normalizeAddress(a domain.Address) domain.Address {
	t := func(s string) string { return spaceRunRE.ReplaceAllString(strings.TrimSpace(s), " ") }
	return domain.Address{Street: t(a.Street), PostalCode: t(a.PostalCode), City: t(a.City), Country: strings.ToUpper(t(a.Country))}
}

func addressIssues(a domain.Address, issues map[string]string) {
	for field, v := range map[string]string{"address.street": a.Street, "address.postal_code": a.PostalCode, "address.city": a.City, "address.country": a.Country} {
		if v == "" {
			issues[field] = "required"
		}
	}
	if a.Country != "" && !countryRE.MatchString(a.Country) {
		issues["address.country"] = "must be a two-letter country code"
	}
}

// orgEmailIssue checks an organization email against the configured domain.
func orgEmailIssue(email, orgDomain string) string {
	at := strings.LastIndex(email, "@")
	if at <= 0 {
		return "must be a valid email address"
	}
	local, dom := email[:at], email[at+1:]
	if !orgLocalRE.MatchString(local) {
		return "may only contain lowercase letters, digits, . - and _"
	}
	if orgDomain != "" && dom != orgDomain {
		return "must end with @" + orgDomain
	}
	if !domainRE.MatchString(dom) {
		return "must be a valid email address"
	}
	return ""
}

// EmailCheck is the result of checking whether an address can receive mail.
type EmailCheck struct {
	Valid bool `json:"valid"`
	// Deliverable is false when the domain has no mail server (MX) or address
	// record; nil when the lookup itself failed (timeout, DNS trouble).
	Deliverable *bool  `json:"deliverable,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

// CheckEmail validates the address and looks up whether its domain accepts
// mail. It never blocks a save: callers show the result as a warning.
func CheckEmail(ctx context.Context, address string) EmailCheck {
	address = strings.ToLower(strings.TrimSpace(address))
	if !validEmail(address) {
		return EmailCheck{Reason: "not a valid email address"}
	}
	dom := address[strings.LastIndex(address, "@")+1:]
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var r net.Resolver
	mx, err := r.LookupMX(ctx, dom)
	if err == nil && len(mx) > 0 && (len(mx) != 1 || mx[0].Host != ".") {
		return EmailCheck{Valid: true, Deliverable: boolPtr(true)}
	}
	if isNotFound(err) || err == nil {
		// RFC 5321: without MX, mail goes to the domain's address record.
		if hosts, herr := r.LookupHost(ctx, dom); herr == nil && len(hosts) > 0 {
			return EmailCheck{Valid: true, Deliverable: boolPtr(true)}
		} else if isNotFound(herr) {
			return EmailCheck{Valid: true, Deliverable: boolPtr(false), Reason: "this domain doesn't seem to receive email"}
		}
	}
	return EmailCheck{Valid: true}
}

func isNotFound(err error) bool {
	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr) && dnsErr.IsNotFound
}

func boolPtr(b bool) *bool { return &b }
