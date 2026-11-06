package service

import (
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/AGlElectronics/hr/backend/internal/domain"
	"github.com/AGlElectronics/hr/backend/internal/provisioners"
)

// Naming templates use {first}, {last} and {nickname}. Rendered values are
// slugged: lowercase ASCII letters, digits, ".", "-" and "_".
const (
	DefaultUsernameTemplate = "{nickname}"
	DefaultOrgEmailTemplate = "{first}.{last}"
	fallbackTemplate        = "{first}.{last}"
)

var (
	placeholderRE  = regexp.MustCompile(`\{[^}]*\}`)
	knownTokens    = map[string]bool{"{first}": true, "{last}": true, "{nickname}": true}
	slugInvalidRE  = regexp.MustCompile(`[^a-z0-9._-]+`)
	slugRepeatRE   = regexp.MustCompile(`([._-])[._-]+`)
	templateCharRE = regexp.MustCompile(`^[A-Za-z0-9._{}-]+$`)
)

// ValidateTemplate reports a problem with a naming template ("" when valid).
func ValidateTemplate(tpl string) string {
	tpl = strings.TrimSpace(tpl)
	if tpl == "" {
		return ""
	}
	if !templateCharRE.MatchString(tpl) {
		return "may only contain letters, digits, . - _ and {first} {last} {nickname}"
	}
	tokens := placeholderRE.FindAllString(tpl, -1)
	if len(tokens) == 0 {
		return "must use at least one of {first} {last} {nickname}"
	}
	for _, t := range tokens {
		if !knownTokens[t] {
			return "unknown placeholder " + t + " (use {first} {last} {nickname})"
		}
	}
	return ""
}

// Slug lowercases, strips accents and apostrophes, and turns spaces into
// dots: "Zoë Müller" -> "zoe.muller", "O'Brien" -> "obrien".
func Slug(s string) string {
	var b strings.Builder
	for _, r := range norm.NFKD.String(strings.ToLower(strings.TrimSpace(s))) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		if unicode.IsSpace(r) {
			r = '.'
		}
		b.WriteRune(r)
	}
	out := slugInvalidRE.ReplaceAllString(b.String(), "")
	out = slugRepeatRE.ReplaceAllString(out, "$1")
	return strings.Trim(out, ".-_")
}

// RenderTemplate fills a template from the person. It returns "" when any
// placeholder it uses is empty, so callers can fall back.
func RenderTemplate(tpl string, p *domain.Person) string {
	missing := false
	out := placeholderRE.ReplaceAllStringFunc(tpl, func(t string) string {
		var v string
		switch t {
		case "{first}":
			v = p.FirstName
		case "{last}":
			v = p.LastName
		case "{nickname}":
			v = p.Nickname
		}
		v = Slug(v)
		if v == "" {
			missing = true
		}
		return v
	})
	if missing {
		return ""
	}
	return Slug(out)
}

// UsernameFor is the login name: the template, then first.last ("" when
// neither can be rendered).
func UsernameFor(tpl string, p *domain.Person) string {
	if strings.TrimSpace(tpl) == "" {
		tpl = DefaultUsernameTemplate
	}
	for _, t := range []string{tpl, fallbackTemplate} {
		if u := RenderTemplate(t, p); u != "" {
			return u
		}
	}
	return ""
}

// templateUses reports whether a template reads the given placeholder.
func templateUses(tpl, token string) bool {
	return strings.Contains(tpl, token)
}

// personAttributes are the profile fields HR mirrors onto the IdP account.
func personAttributes(p *domain.Person) map[string]string {
	return map[string]string{
		provisioners.AttrNickname:     p.Nickname,
		provisioners.AttrPhone:        p.Phone,
		provisioners.AttrOrgEmail:     p.OrgEmail,
		provisioners.AttrPrivateEmail: p.Email,
	}
}
