package service

import "testing"

func TestValidName(t *testing.T) {
	for _, ok := range []string{"Zoë", "Anna Maria", "Jean-Luc", "O'Brien", "J. R.", "Łukasz", "Ægir"} {
		if !validName(cleanName(ok)) {
			t.Errorf("%q rejected", ok)
		}
	}
	for _, bad := range []string{"Fox🦊", "R2D2", "a{b}", "-Anna", "x:y", "\"Q\"", "a?b", ".Max"} {
		if validName(cleanName(bad)) {
			t.Errorf("%q accepted", bad)
		}
	}
	if got := cleanName("  O’Brien   Smith "); got != "O'Brien Smith" {
		t.Errorf("cleanName: %q", got)
	}
}

func TestNormalizePhone(t *testing.T) {
	cases := []struct{ raw, country, want string }{
		{"+43 660 1234567", "", "+436601234567"},
		{"0043 660 1234567", "", "+436601234567"},
		{"0660 1234567", "AT", "+436601234567"},
		{"0660 1234567", "", ""},
		{"+999 123", "AT", ""},
		{"12", "AT", ""},
	}
	for _, c := range cases {
		if got := NormalizePhone(c.raw, c.country); got != c.want {
			t.Errorf("NormalizePhone(%q, %q) = %q, want %q", c.raw, c.country, got, c.want)
		}
	}
}

func TestOrgEmailIssue(t *testing.T) {
	if msg := orgEmailIssue("anna.lena@example.org", "example.org"); msg != "" {
		t.Errorf("valid org email rejected: %s", msg)
	}
	if orgEmailIssue("anna@other.org", "example.org") == "" {
		t.Error("other domain accepted")
	}
	if orgEmailIssue("an na@example.org", "example.org") == "" {
		t.Error("space accepted")
	}
}

func TestValidEmail(t *testing.T) {
	for _, ok := range []string{"a@b.co", "first.last+tag@example.org"} {
		if !validEmail(ok) {
			t.Errorf("%q rejected", ok)
		}
	}
	for _, bad := range []string{"a@b", "a b@c.de", "zoë@example.org", "a@-x.com", "a@b.c"} {
		if validEmail(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}
