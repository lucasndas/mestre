package tables

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/localchan"
)

// The reach of the reading of `table overlap`: how many commits of the base it looks
// at and how many it shows.
const (
	overlapScan = 200
	overlapShow = 20
)

// BaseCommit is a commit of the base that touched the paths asked.
type BaseCommit struct {
	Hash, Subject, Author string
	When                  time.Time
	Files                 []string
}

// TableOverlap is a table that changes files in the paths asked.
type TableOverlap struct {
	Table string
	Epic  int
	Files []string
}

// Overlap is what `table overlap` answers.
type Overlap struct {
	Base    string
	Commits []BaseCommit
	Tables  []TableOverlap
}

// globRegexp turns a glob into a regular expression: `**` crosses folders, `*`
// and `?` stay inside one.
func globRegexp(glob string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(glob); i++ {
		switch c := glob[i]; c {
		case '*':
			if i+1 < len(glob) && glob[i+1] == '*' {
				i++
				if i+1 < len(glob) && glob[i+1] == '/' {
					i++
					b.WriteString("(?:.*/)?")
				} else {
					b.WriteString(".*")
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

// pathFilter says whether a file is under one of the globs (any file, with none).
func pathFilter(globs []string) (func(string) bool, error) {
	if len(globs) == 0 {
		return func(string) bool { return true }, nil
	}
	var res []*regexp.Regexp
	for _, g := range globs {
		re, err := globRegexp(strings.TrimPrefix(g, "./"))
		if err != nil {
			return nil, fmt.Errorf("%q is not a glob: %w", g, err)
		}
		res = append(res, re)
	}
	return func(path string) bool {
		for _, re := range res {
			if re.MatchString(path) {
				return true
			}
		}
		return false
	}, nil
}

func filesOf(changes []gitx.FileChange, keep func(string) bool) []string {
	var out []string
	for _, c := range changes {
		if keep(c.Path) {
			out = append(out, c.Path)
		}
	}
	return out
}

// Overlap is the reading for deciding, before a table is opened, whether the work
// already exists or collides: the recent commits of the base that touch the paths
// and, for each table that is not over, the files its branch changes in them. It
// reads the mirror and changes nothing; the engine does not judge
// (docs/cli/grao-mestre.md#mestre-table).
func (s *Service) Overlap(ctx context.Context, workspace string, globs []string) (Overlap, error) {
	keep, err := pathFilter(globs)
	if err != nil {
		return Overlap{}, err
	}
	w, err := s.o.Workspaces.Get(ctx, workspace)
	if err != nil {
		return Overlap{}, err
	}
	mirror, err := s.o.Workspaces.OpenMirror(ctx, workspace)
	if err != nil {
		return Overlap{}, err
	}
	out := Overlap{Base: w.DefaultBranch}
	tip, err := mirror.ResolveRef(ctx, "refs/remotes/origin/"+w.DefaultBranch)
	if err != nil {
		return Overlap{}, fmt.Errorf("a branch base origin/%s não foi lida: %w", w.DefaultBranch, err)
	}
	log, err := mirror.Log(ctx, string(tip))
	if err != nil {
		return Overlap{}, err
	}
	for i, c := range log {
		if i >= overlapScan || len(out.Commits) >= overlapShow {
			break
		}
		if len(c.Parents) == 0 {
			continue
		}
		changes, err := mirror.DiffNameStatus(ctx, string(c.Parents[0]), string(c.Hash))
		if err != nil {
			return Overlap{}, err
		}
		if files := filesOf(changes, keep); len(files) > 0 {
			out.Commits = append(out.Commits, BaseCommit{Hash: string(c.Hash), Subject: c.Subject(), Author: c.Author.Name, When: c.Author.When, Files: files})
		}
	}
	ts, err := s.store.List(ctx, workspace)
	if err != nil {
		return Overlap{}, err
	}
	for _, t := range ts {
		if Over(t.State) {
			continue
		}
		head, err := mirror.ResolveRef(ctx, "refs/heads/"+t.Branch)
		if err != nil || t.BaseCommit == "" {
			continue // a table whose branch is not in the mirror has no file to show
		}
		changes, err := mirror.DiffNameStatus(ctx, t.BaseCommit, string(head))
		if err != nil {
			return Overlap{}, err
		}
		if files := filesOf(changes, keep); len(files) > 0 {
			out.Tables = append(out.Tables, TableOverlap{Table: t.ID, Epic: t.Epic, Files: files})
		}
	}
	return out, nil
}

func (s *Service) handleOverlap(ctx context.Context, c *localchan.Call) localchan.Response {
	gm, refused := s.caller(ctx, c)
	if refused != nil {
		return *refused
	}
	if args, ok := c.Request.Strings(); !ok || len(args) != 0 {
		return localchan.Fail(localchan.CodeUsage, `usage: mestre table overlap [--path "<glob>"]…`, "")
	}
	var globs []string
	switch v := c.Request.Flags["path"].(type) {
	case []string:
		globs = v
	case []any:
		for _, x := range v {
			if g, ok := x.(string); ok {
				globs = append(globs, g)
			}
		}
	}
	res, err := s.Overlap(ctx, gm.Workspace, globs)
	if err != nil {
		return localchan.Fail(localchan.CodeUsage, err.Error(), "")
	}
	commits := make([]map[string]any, 0, len(res.Commits))
	var text strings.Builder
	fmt.Fprintf(&text, "Base %s: %d commits recentes nos caminhos", res.Base, len(res.Commits))
	for _, c := range res.Commits {
		commits = append(commits, map[string]any{"hash": c.Hash, "subject": c.Subject, "author": c.Author, "when": c.When.UTC().Format(time.RFC3339), "files": c.Files})
		fmt.Fprintf(&text, "\n- %.8s %s (%s)", c.Hash, oneLine(c.Subject), strings.Join(c.Files, ", "))
	}
	tables := make([]map[string]any, 0, len(res.Tables))
	for _, t := range res.Tables {
		tables = append(tables, map[string]any{"name": t.Table, "epic": t.Epic, "files": t.Files})
		fmt.Fprintf(&text, "\nMesa %s (épico #%d): %s", t.Table, t.Epic, strings.Join(t.Files, ", "))
	}
	if globs == nil {
		globs = []string{}
	}
	return localchan.OK(map[string]any{"base": res.Base, "paths": globs, "commits": commits, "tables": tables, "text": text.String()})
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
