package main

import (
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// QuarantinePath is the quarantine file (docs/qualidade/testes.md#quarentena).
const QuarantinePath = "test/quarantine.yaml"

// QuarantineDays is the default time a test may stay in quarantine.
const QuarantineDays = 14

// QuarantineEntry is one quarantined test. Test is the top-level Go test
// name, as used by `go test -skip`.
type QuarantineEntry struct {
	Test   string `yaml:"test"`  // for suite web: a Vitest test file, relative to web/
	Suite  string `yaml:"suite"` // fast, full (default), heavy: the build tags `testrun quarantine` runs it with; web: a Vitest file
	Issue  string `yaml:"issue"`
	Added  string `yaml:"added"` // AAAA-MM-DD
	Reason string `yaml:"reason"`
}

type quarantineFile struct {
	Tests []QuarantineEntry `yaml:"tests"`
}

var topLevelTestRe = regexp.MustCompile(`^Test[A-Za-z0-9_]*$`)

// loadQuarantine reads the quarantine file; a missing file is empty.
func loadQuarantine(fsys fs.FS) ([]QuarantineEntry, error) {
	b, err := fs.ReadFile(fsys, QuarantinePath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", QuarantinePath, err)
	}
	var f quarantineFile
	if err := yaml.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", QuarantinePath, err)
	}
	for i, e := range f.Tests {
		switch {
		case e.Suite == webSuite:
			if e.Test == "" || strings.ContainsAny(e.Test, `\ `) || strings.HasPrefix(e.Test, "/") {
				return nil, fmt.Errorf("%s: entry %d: web test %q is not a file path relative to web/, with slashes", QuarantinePath, i+1, e.Test)
			}
			if e.Issue == "" || e.Reason == "" {
				return nil, fmt.Errorf("%s: entry %d (%s): issue and reason are required", QuarantinePath, i+1, e.Test)
			}
		case !topLevelTestRe.MatchString(e.Test):
			return nil, fmt.Errorf("%s: entry %d: test %q is not a top-level Go test name", QuarantinePath, i+1, e.Test)
		case e.Suite != "" && Suites[e.Suite].Name == "":
			return nil, fmt.Errorf("%s: entry %d (%s): suite %q is not fast, full, heavy or web", QuarantinePath, i+1, e.Test, e.Suite)
		case e.Issue == "" || e.Reason == "":
			return nil, fmt.Errorf("%s: entry %d (%s): issue and reason are required", QuarantinePath, i+1, e.Test)
		}
		if _, err := time.Parse(time.DateOnly, e.Added); err != nil {
			return nil, fmt.Errorf("%s: entry %d (%s): added %q is not AAAA-MM-DD", QuarantinePath, i+1, e.Test, e.Added)
		}
	}
	return f.Tests, nil
}

// suiteName is the suite whose build tags run the entry: full unless the
// file says otherwise (full compiles the fast tests too).
func (e QuarantineEntry) suiteName() string {
	if e.Suite == "" {
		return "full"
	}
	return e.Suite
}

// expired reports whether the entry is past its deadline on today.
func (e QuarantineEntry) expired(today time.Time) bool {
	added, _ := time.Parse(time.DateOnly, e.Added)
	deadline := added.AddDate(0, 0, QuarantineDays).Format(time.DateOnly)
	return today.Format(time.DateOnly) > deadline
}

func quarantineNames(es []QuarantineEntry) []string {
	names := make([]string, len(es))
	for i, e := range es {
		names[i] = e.Test
	}
	return names
}

// skipPattern is the -skip value for the entries.
func skipPattern(es []QuarantineEntry) string {
	if len(es) == 0 {
		return ""
	}
	return "^(" + strings.Join(quarantineNames(es), "|") + ")$"
}
