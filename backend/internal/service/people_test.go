package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/AGlElectronics/hr/backend/internal/domain"
)

func TestPersonDiff(t *testing.T) {
	prev := &domain.Person{FirstName: "Ada", Nickname: "ada", Email: "ada@example.org", Phone: "+441", Status: domain.PersonActive}
	next := *prev
	next.Nickname, next.Phone, next.IsBanned = "countess", "+442", true
	d := PersonDiff(prev, &next)
	raw, _ := json.Marshal(d)
	s := string(raw)
	if len(d) != 3 || !strings.Contains(s, `"nickname":{"from":"ada","to":"countess"}`) ||
		!strings.Contains(s, `"is_banned":{"from":false,"to":true}`) || !strings.Contains(s, `"phone":{"changed":true}`) {
		t.Fatalf("diff = %s", s)
	}
	if strings.Contains(s, "+44") {
		t.Fatalf("sensitive value leaked into the diff: %s", s)
	}
	if d := PersonDiff(prev, prev); len(d) != 0 {
		t.Fatalf("no change gave %v", d)
	}
}

func TestCriticalChanges(t *testing.T) {
	prev := &domain.Person{FirstName: "Ada", LastName: "Lovelace", Nickname: "ada", Email: "ada@example.org"}
	in := PersonInput{FirstName: "Ada", LastName: "Lovelace", Nickname: "ada", Email: "ADA@example.org ", Phone: "+44 20 7946 0000"}
	if got := CriticalChanges(prev, in); len(got) != 0 {
		t.Fatalf("phone and email case are not critical, got %v", got)
	}
	in.Nickname = "countess"
	if got := CriticalChanges(prev, in); len(got) != 1 || got[0] != "nickname" {
		t.Fatalf("nickname change: %v", got)
	}
}
