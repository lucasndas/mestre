package quests

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// DefaultMaxReturns is the number of returns a step that hands work back may
// make when the definition does not say (docs/orquestracao/missoes.md#definição).
const DefaultMaxReturns = 3

// DefaultStuckAfter is how long the character of a step may stay idle with no
// done before the step is stuck.
const DefaultStuckAfter = 20 * time.Minute

// The values of the fields that take a fixed set.
const (
	JoinAll = "all"
	JoinAny = "any"

	ApprovalBefore = "before"
	ApprovalAfter  = "after"
)

// Definition is a quest as the Master of a table (or a user on the canvas)
// writes it, in YAML or JSON (docs/orquestracao/missoes.md#definição).
type Definition struct {
	Name string `yaml:"name" json:"name"`
	// MaxReturns is how many times a step may hand the work back; nil is
	// DefaultMaxReturns.
	MaxReturns *int      `yaml:"max_returns,omitempty" json:"max_returns,omitempty"`
	Steps      []StepDef `yaml:"steps" json:"steps"`
}

// StepDef is one step of a quest.
type StepDef struct {
	ID   string `yaml:"id" json:"id"`
	Name string `yaml:"name,omitempty" json:"name,omitempty"`
	// Sheet makes a character for the step; Character names one of the table
	// that does it.
	Sheet     string `yaml:"sheet,omitempty" json:"sheet,omitempty"`
	Character string `yaml:"character,omitempty" json:"character,omitempty"`
	// Class replaces the class of the sheet, Machine pins the machine.
	Class        string   `yaml:"class,omitempty" json:"class,omitempty"`
	Machine      string   `yaml:"machine,omitempty" json:"machine,omitempty"`
	Instructions string   `yaml:"instructions,omitempty" json:"instructions,omitempty"`
	After        []string `yaml:"after,omitempty" json:"after,omitempty"`
	// Join is "all" (the default) or "any".
	Join        string `yaml:"join,omitempty" json:"join,omitempty"`
	CanReturnTo string `yaml:"can_return_to,omitempty" json:"can_return_to,omitempty"`
	// Approval is "before" or "after".
	Approval string `yaml:"approval,omitempty" json:"approval,omitempty"`
	SubIssue bool   `yaml:"sub_issue,omitempty" json:"sub_issue,omitempty"`
	// StuckAfter is a duration such as "30m"; empty is DefaultStuckAfter.
	StuckAfter string `yaml:"stuck_after,omitempty" json:"stuck_after,omitempty"`
}

// Title is the name shown for the step: the name, or the id.
func (s StepDef) Title() string {
	if s.Name != "" {
		return s.Name
	}
	return s.ID
}

// JoinAny says whether the step waits for the first of its previous steps.
func (s StepDef) JoinAny() bool { return s.Join == JoinAny }

// StuckTime is the idle time without a done after which the step is stuck.
func (s StepDef) StuckTime() time.Duration {
	if d, err := time.ParseDuration(s.StuckAfter); err == nil && d > 0 {
		return d
	}
	return DefaultStuckAfter
}

// Max is the limit of returns of the quest.
func (d Definition) Max() int {
	if d.MaxReturns == nil {
		return DefaultMaxReturns
	}
	return *d.MaxReturns
}

// Index is the position of the step in the definition, or -1.
func (d Definition) Index(id string) int {
	return slices.IndexFunc(d.Steps, func(s StepDef) bool { return s.ID == id })
}

// Step returns the step with the id.
func (d Definition) Step(id string) (StepDef, bool) {
	if i := d.Index(id); i >= 0 {
		return d.Steps[i], true
	}
	return StepDef{}, false
}

// Dependents lists the ids of the steps that depend on id, directly or not,
// in the order of the definition.
func (d Definition) Dependents(id string) []string {
	reach := map[string]bool{id: true}
	var out []string
	for _, s := range d.Steps { // after only points back, so one pass is enough
		for _, a := range s.After {
			if reach[a] {
				reach[s.ID] = true
				out = append(out, s.ID)
				break
			}
		}
	}
	return out
}

// Ancestors says whether target is a step that id depends on, directly or not.
func (d Definition) Ancestors(id, target string) bool {
	s, ok := d.Step(id)
	if !ok {
		return false
	}
	for _, a := range s.After {
		if a == target || d.Ancestors(a, target) {
			return true
		}
	}
	return false
}

// Parse reads a definition from YAML or JSON (JSON is YAML). A field the
// format does not know is an error, so a typo does not silently change a quest.
func Parse(data []byte) (Definition, error) {
	var d Definition
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&d); err != nil {
		if errors.Is(err, io.EOF) {
			return Definition{}, &ValidationError{Problems: []Problem{{Message: "the definition is empty"}}}
		}
		return Definition{}, &ValidationError{Problems: []Problem{{Message: "the definition cannot be read: " + err.Error()}}}
	}
	return d, nil
}

// Problem is one thing wrong in a definition; Step is the id of the step it
// is about ("" for the quest).
type Problem struct {
	Step    string
	Message string
}

func (p Problem) String() string {
	if p.Step == "" {
		return p.Message
	}
	return fmt.Sprintf("step %q: %s", p.Step, p.Message)
}

// ValidationError is a definition that is refused, with every problem found.
type ValidationError struct{ Problems []Problem }

func (e *ValidationError) Error() string {
	parts := make([]string, len(e.Problems))
	for i, p := range e.Problems {
		parts[i] = p.String()
	}
	return "invalid quest: " + strings.Join(parts, "; ")
}

// Has says whether a problem is about the step.
func (e *ValidationError) Has(step string) bool {
	return slices.ContainsFunc(e.Problems, func(p Problem) bool { return p.Step == step })
}

// Validate checks what a definition says by itself: unique ids, steps that
// point only back, no step pointing at itself or at one that is not before it
// (so no cycles), a single place for the character, the fixed values, the
// returns to a step the returner depends on. Whether sheets and characters
// exist, and the ceiling, is the Service's (it knows the catalog).
func (d Definition) Validate() error {
	var ps []Problem
	add := func(step, format string, a ...any) {
		ps = append(ps, Problem{Step: step, Message: fmt.Sprintf(format, a...)})
	}
	if strings.TrimSpace(d.Name) == "" {
		add("", "the quest has no name")
	}
	if d.MaxReturns != nil && *d.MaxReturns < 0 {
		add("", "max_returns cannot be negative")
	}
	if len(d.Steps) == 0 {
		add("", "the quest has no steps")
	}
	pos := map[string]int{}
	names := map[string]string{}
	for i, s := range d.Steps {
		switch {
		case s.ID == "":
			add(fmt.Sprintf("#%d", i+1), "the step has no id")
			continue
		case strings.ContainsAny(s.ID, " /\t\n"):
			add(s.ID, "the id has a space or a slash")
		}
		if _, dup := pos[s.ID]; dup {
			add(s.ID, "the id is used by another step")
			continue
		}
		pos[s.ID] = i
		if other, dup := names[strings.ToLower(s.Title())]; dup {
			add(s.ID, "the name %q is the name of step %q", s.Title(), other)
		}
		names[strings.ToLower(s.Title())] = s.ID
	}
	for i, s := range d.Steps {
		if s.ID == "" {
			continue
		}
		switch {
		case s.Sheet == "" && s.Character == "":
			add(s.ID, "it needs a sheet or a character")
		case s.Sheet != "" && s.Character != "":
			add(s.ID, "it has a sheet and a character; pick one")
		}
		for _, a := range s.After {
			p, ok := pos[a]
			switch {
			case !ok:
				add(s.ID, "after: %q is not a step", a)
			case p >= i:
				add(s.ID, "after: %q is not before this step", a)
			}
		}
		if s.CanReturnTo != "" {
			p, ok := pos[s.CanReturnTo]
			switch {
			case !ok:
				add(s.ID, "can_return_to: %q is not a step", s.CanReturnTo)
			case p >= i:
				add(s.ID, "can_return_to: %q is not before this step", s.CanReturnTo)
			case !d.Ancestors(s.ID, s.CanReturnTo):
				add(s.ID, "can_return_to: %q is not a step this one depends on", s.CanReturnTo)
			}
		}
		if !slices.Contains([]string{"", JoinAll, JoinAny}, s.Join) {
			add(s.ID, "join: %q is not all or any", s.Join)
		}
		if !slices.Contains([]string{"", ApprovalBefore, ApprovalAfter}, s.Approval) {
			add(s.ID, "approval: %q is not before or after", s.Approval)
		}
		if s.StuckAfter != "" {
			if t, err := time.ParseDuration(s.StuckAfter); err != nil || t <= 0 {
				add(s.ID, "stuck_after: %q is not a duration", s.StuckAfter)
			}
		}
	}
	if len(ps) > 0 {
		return &ValidationError{Problems: ps}
	}
	return nil
}
