package sessionzero_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/mestre-ia/mestre/internal/sessionzero"
)

// The gitconfig of a character is in a folder the agent writes: whatever it
// left there between two starts is replaced by the engine's at the next start.
func TestTheGitconfigEditedByTheAgentIsRewrittenAtTheNextStart(t *testing.T) {
	t.Parallel()
	r := newCharRig(t, nil)
	q := r.req("dev", "claude-code", sessionzero.RoleCharacter)
	plan := r.prepare(q)
	want, err := os.ReadFile(plan.GitConfig)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plan.GitConfig, []byte("[user]\n\tname = Someone Else\n\temail = x@y.z\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	q.ResumeSessionID, q.NewSessionID = "11111111-2222-4333-8444-555555555555", ""
	r.prepare(q)
	got, err := os.ReadFile(plan.GitConfig)
	if err != nil || string(got) != string(want) {
		t.Fatalf("gitconfig after the new start = %q (%v), want %q", got, err, want)
	}
}

// The detector of secrets is required: without it the environment of a
// character would not be checked, so there is no session zero of characters.
func TestACharacterWithoutTheDetectorOfSecretsDoesNotStart(t *testing.T) {
	t.Parallel()
	r := newCharRig(t, nil)
	o := r.opts
	o.Masker = nil
	if _, err := sessionzero.NewCharacter(o); err == nil {
		t.Fatal("NewCharacter accepted a nil Masker")
	}
}

// After a restart of the engine the session zero is empty; Restore brings back
// a living character from the request the runtime kept (as JSON), and then the
// layer is refreshed and the identity updated as before.
func TestRestoreBringsBackALivingCharacterAfterARestart(t *testing.T) {
	t.Parallel()
	for _, agent := range threeAgents {
		r := newCharRig(t, nil)
		q := r.req("dev", agent, sessionzero.RoleCharacter)
		q.ProviderKey = nil
		r.prepare(q)
		raw, err := json.Marshal(q)
		if err != nil {
			t.Fatal(err)
		}

		r.rebuildChar() // the engine restarted: nothing in memory, the births stay
		if _, err := r.ch.UpdateIdentity(r.ctx, "dev", devIdentity); err == nil {
			t.Fatalf("%s: UpdateIdentity worked on a character nobody prepared", agent)
		}
		var back sessionzero.CharacterRequest
		if err := json.Unmarshal(raw, &back); err != nil {
			t.Fatal(err)
		}
		if err := r.ch.Restore(r.ctx, back); err != nil {
			t.Fatalf("%s: Restore: %v", agent, err)
		}
		next := sessionzero.Identity{Name: "lucas-mestre-qa[bot]", Email: "456+lucas-mestre-qa[bot]@users.noreply.github.com"}
		if changed, err := r.ch.UpdateIdentity(r.ctx, "dev", next); err != nil || !changed {
			t.Errorf("%s: UpdateIdentity after the restart = %v, %v", agent, changed, err)
		}
		d := sessionzero.Dynamic{Instructions: "Be brief."}
		if _, err := r.ch.Refresh(r.ctx, "dev", d); err != nil {
			t.Errorf("%s: Refresh after the restart: %v", agent, err)
		}
	}
}
