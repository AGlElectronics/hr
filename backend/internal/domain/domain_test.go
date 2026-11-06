package domain

import "testing"

func TestCanTransition_Allowed(t *testing.T) {
	// Each pair represents an edge that MUST be legal. Keeping these as
	// data (instead of switch cases) makes it obvious at a glance which
	// transitions the product supports.
	cases := []struct {
		from, to DraftState
	}{
		{DraftStateDraft, DraftStateProvisioning},
		{DraftStateProvisioning, DraftStateActive},
		{DraftStateProvisioning, DraftStateRejected},
		{DraftStateProvisioning, DraftStateDraft},
	}
	for _, c := range cases {
		if !CanTransition(c.from, c.to) {
			t.Errorf("expected %q -> %q to be allowed", c.from, c.to)
		}
	}
}

func TestCanTransition_Disallowed(t *testing.T) {
	// A representative sample of transitions that must be rejected:
	//   - skipping over provisioning
	//   - jumping back from terminal states
	//   - leaving active (terminal)
	//   - unknown state names
	cases := []struct {
		from, to DraftState
	}{
		{DraftStateDraft, DraftStateActive},
		{DraftStateActive, DraftStateRejected},
		{DraftStateRejected, DraftStateDraft},
		{DraftState("bogus"), DraftStateActive},
	}
	for _, c := range cases {
		if CanTransition(c.from, c.to) {
			t.Errorf("expected %q -> %q to be rejected", c.from, c.to)
		}
	}
}

func TestTransition_ReturnsErrorOnIllegalMove(t *testing.T) {
	if _, err := Transition(DraftStateActive, DraftStateDraft); err == nil {
		t.Fatal("expected error on illegal transition active -> draft")
	}
	if got, err := Transition(DraftStateDraft, DraftStateProvisioning); err != nil || got != DraftStateProvisioning {
		t.Fatalf("unexpected: got=%q err=%v", got, err)
	}
}

func TestIsTerminal(t *testing.T) {
	if !DraftStateActive.IsTerminal() {
		t.Error("active should be terminal")
	}
	if !DraftStateRejected.IsTerminal() {
		t.Error("rejected should be terminal")
	}
	if DraftStateDraft.IsTerminal() {
		t.Error("draft should not be terminal")
	}
}
