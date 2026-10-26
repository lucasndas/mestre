package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/mestre-ia/mestre/internal/providers"
)

// hookFiles is `hookfiles`: it writes, in the worktree -root, the hook files
// the adapter of -provider generates calling -mestre, merged over what is
// there, and nothing else. The Windows round runs a real CLI against them,
// without the engine, to see what the line does when the mestre cannot
// answer (tools/winsandbox/round/run-all.ps1, item 4). It uses no login.
func hookFiles(args []string, out io.Writer) int {
	fl := flag.NewFlagSet("conformance hookfiles", flag.ContinueOnError)
	fl.SetOutput(out)
	provider := fl.String("provider", "", "claude-code, codex or antigravity")
	root := fl.String("root", "", "the worktree")
	mestre := fl.String("mestre", "", "the stable path of the mestre binary the hooks call (it need not exist)")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	if *provider == "" || *root == "" || *mestre == "" {
		fmt.Fprintln(out, "usage: conformance hookfiles -provider <id> -root <worktree> -mestre <path>")
		return 2
	}
	if err := writeHookFiles(providers.ID(*provider), *root, *mestre); err != nil {
		fmt.Fprintln(out, "conformance hookfiles:", err)
		return 1
	}
	return 0
}

func writeHookFiles(id providers.ID, root, mestre string) error {
	a, err := providers.NewRegistry(providers.Options{}).For(id)
	if err != nil {
		return err
	}
	files, err := a.HookFiles(providers.HookFilesRequest{Root: root, Binary: mestre})
	if err != nil {
		return err
	}
	for _, f := range files {
		cur, err := os.ReadFile(f.Path)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		b, err := f.Merge(cur)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(f.Path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(f.Path, b, 0o644); err != nil {
			return err
		}
	}
	return nil
}
