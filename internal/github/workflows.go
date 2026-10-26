package github

import (
	"errors"
	"path"
	"strings"

	"github.com/mestre-ia/mestre/internal/classes"
)

// The errors of the push of the CI.
var (
	// ErrWorkflowNotAllowed: the class has `git.workflows: never` and the branch
	// changes `.github/workflows/` (code `workflow_not_allowed`).
	ErrWorkflowNotAllowed = errors.New("github: the class may not change the workflows of the repository")
	// ErrWorkflowPermission: GitHub would refuse the push because the App has no
	// Workflows permission yet (code `workflow_permission`).
	ErrWorkflowPermission = errors.New("github: the App has no Workflows permission")
)

// TouchesWorkflows says whether any of the changed files is in
// `.github/workflows/`.
func TouchesWorkflows(changed []string) bool {
	for _, f := range changed {
		if strings.HasPrefix(f, `"`) {
			return true // a name that git quoted (bytes outside ASCII): not read, so taken as a change of the CI
		}
		f = path.Clean(strings.TrimLeft(strings.ReplaceAll(f, `\`, "/"), "/"))
		if strings.HasPrefix(strings.ToLower(f), ".github/workflows/") || strings.EqualFold(f, ".github/workflows") {
			return true
		}
	}
	return false
}

// PushDecision is what the engine does with a push that may change the CI.
type PushDecision int

// The decisions.
const (
	// PushAllowed: nothing of the CI changes, or the class may change it.
	PushAllowed PushDecision = iota
	// PushNeedsApproval: a request of type `envio_workflows` is made and the push
	// waits for the "Aprovar" of a user, on the hash approved.
	PushNeedsApproval
)

// CheckPush decides the push of a branch from the files it changes and the
// `git.workflows` of the class (limited by the ceiling, default `never`). With
// `never` and a change in the workflows the engine does not push
// (ErrWorkflowNotAllowed) and tells the Grand Master.
func CheckPush(changed []string, p classes.Workflows) (PushDecision, error) {
	if !TouchesWorkflows(changed) {
		return PushAllowed, nil
	}
	switch p {
	case classes.WorkflowsAllow:
		return PushAllowed, nil
	case classes.WorkflowsAsk:
		return PushNeedsApproval, nil
	}
	return PushAllowed, ErrWorkflowNotAllowed // never, or anything the engine does not know
}

// NeedsWorkflowsPermission says whether the App of a class is made with the
// Workflows permission: `ask` and `allow` yes, `never` no. The permission is only
// the technical capacity; the rule of the class decides if it is used.
func NeedsWorkflowsPermission(p classes.Workflows) bool {
	return p == classes.WorkflowsAsk || p == classes.WorkflowsAllow
}

// CheckApp is the check before the push: the App that will push must have the
// permission when the branch changes the CI. A class moved from `never` to `ask`
// gets the permission in the App only after the Owner accepts it on GitHub, so
// until then the push stops here with ErrWorkflowPermission.
func CheckApp(app App, changed []string) error {
	if TouchesWorkflows(changed) && !app.Workflows {
		return ErrWorkflowPermission
	}
	return nil
}
