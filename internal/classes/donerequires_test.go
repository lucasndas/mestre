package classes_test

import (
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
)

func doneClass(t *testing.T, front, perms string) classes.Examination {
	t.Helper()
	files, err := classes.NewFiles([]classes.File{
		{Path: "CLASS.md", Data: []byte("---\nname: qa\ndescription: Testa a mesa\napp: novo\n" + front + "---\ntexto")},
		{Path: "permissions.yaml", Data: []byte(perms)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return classes.Examine(files, classes.ExamineOptions{Scope: classes.ClassTable, Dir: "qa"})
}

func problems(ex classes.Examination) string {
	var all []string
	for _, p := range ex.Problems {
		all = append(all, p.String())
	}
	return strings.Join(all, "\n")
}

// CA-PROVA-12, the half of the class
func TestDoneRequiresIsReadAndAClassThatVerifiesMustBeAbleToCommit(t *testing.T) {
	t.Parallel()
	ex := doneClass(t, "done_requires:\n  proof: all\n  verify: true\n", defaultPerms)
	if !ex.OK() {
		t.Fatalf("a valid done_requires was refused: %s", problems(ex))
	}
	if d := ex.Class.Meta.Done(); d.Proof != classes.ProofAll || !d.Verify {
		t.Errorf("done_requires = %+v", d)
	}
	if d := doneClass(t, "", defaultPerms).Class.Meta.Done(); d.Proof != classes.ProofNone || d.Verify {
		t.Errorf("the default done_requires = %+v, want none and no verification", d)
	}
	ex = doneClass(t, "done_requires: {verify: true}\n", "version: 1\ngit: {commit: false}\n")
	if ex.OK() || !strings.Contains(problems(ex), "git.commit") {
		t.Errorf("verify: true with git.commit: false was accepted: %s", problems(ex))
	}
	// without verification a class that does not commit is fine
	if ex = doneClass(t, "done_requires: {proof: all}\n", "version: 1\ngit: {commit: false}\n"); !ex.OK() {
		t.Errorf("proof: all with no commits was refused: %s", problems(ex))
	}
	for name, front := range map[string]string{
		"value out of the list": "done_requires: {proof: some}\n",
		"unknown key":           "done_requires: {proofs: all}\n",
	} {
		if ex := doneClass(t, front, defaultPerms); ex.OK() {
			t.Errorf("%s was accepted", name)
		}
	}
}
