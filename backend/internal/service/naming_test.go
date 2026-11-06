package service

import (
	"strings"
	"testing"

	"github.com/AGlElectronics/hr/backend/internal/domain"
)

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"Fluffy Tail":       "fluffy.tail",
		"  Zoë  ":           "zoe",
		"Ünïcødé--Fox!":     "unicde-fox",
		"a..b__c":           "a.b_c",
		"Anna-Lena O'Brien": "anna-lena.obrien",
		"":                  "",
	}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUsernameFor(t *testing.T) {
	p := &domain.Person{FirstName: "Jane", LastName: "Doe", Nickname: "Sparky Paws", Email: "Jane@Example.org"}
	if got := UsernameFor("{nickname}", p); got != "sparky.paws" {
		t.Errorf("nickname template: %q", got)
	}
	if got := UsernameFor("{first}.{last}", p); got != "jane.doe" {
		t.Errorf("first.last template: %q", got)
	}
	p.Nickname = ""
	if got := UsernameFor("{nickname}", p); got != "jane.doe" {
		t.Errorf("fallback without nickname: %q", got)
	}
	p.FirstName, p.LastName = "", ""
	if got := UsernameFor("{nickname}", p); got != "" {
		t.Errorf("no name should give no username: %q", got)
	}
}

func TestValidateTemplate(t *testing.T) {
	for _, ok := range []string{"", "{nickname}", "{first}.{last}", "{first}-{nickname}"} {
		if msg := ValidateTemplate(ok); msg != "" {
			t.Errorf("%q rejected: %s", ok, msg)
		}
	}
	for _, bad := range []string{"{email}", "plain", "{first} {last}"} {
		if ValidateTemplate(bad) == "" {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestDeletedNotice(t *testing.T) {
	p := &domain.Person{FirstName: "Jane", Nickname: "Sparky", Email: "jane@example.org"}
	m := DeletedNotice("Example Con", p, OffboardInput{Reason: "left", ShareReason: false})
	if m.To != "jane@example.org" || !strings.Contains(m.Subject, "Example Con") || !strings.Contains(m.Body, "Hi Sparky") {
		t.Fatalf("unexpected message: %+v", m)
	}
	if strings.Contains(m.Body, "left") {
		t.Error("reason leaked without ShareReason")
	}
	m = DeletedNotice("", p, OffboardInput{Reason: "left", ShareReason: true})
	if !strings.Contains(m.Body, "Reason: left") || strings.Contains(m.Subject, "  ") {
		t.Fatalf("unexpected message: %+v", m)
	}
}
