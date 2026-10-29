package verification

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/safefile"
)

// File is the file of the proposal, relative to the main clone of the workspace.
const File = ".mestre/regras/verificacao.yaml"

// Keeper keeps the proposal of the Grand Master in File and tells the
// Administrators it waits for the confirmation (Store.Confirm). It implements
// sessionzero.VerificationProposer.
type Keeper struct {
	// Notify tells the Administrators of the workspace; its failure does not fail
	// the stage (the file is written and a new run would not tell again).
	Notify func(ctx context.Context, workspace string, msg i18n.Msg) error
	Log    *slog.Logger
}

// Propose writes the commands as a proposal when the file is absent or blank. A
// file with content (an earlier proposal, or what people wrote) is left alone.
func (k Keeper) Propose(ctx context.Context, workspace, dir string, commands []string) error {
	if len(commands) == 0 {
		return nil
	}
	doc, err := yaml.Marshal(struct {
		Version  int      `yaml:"version"`
		Comandos []string `yaml:"comandos"`
		Timeout  string   `yaml:"timeout"`
	}{1, commands, DefaultTimeout.String()})
	if err != nil {
		return err
	}
	if _, err := Parse(doc); err != nil {
		return err
	}
	path := filepath.Join(dir, filepath.FromSlash(File))
	cur, _, err := safefile.Read(path, 16<<10)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read %s: %w", File, err)
	}
	if len(bytes.TrimSpace(cur)) > 0 {
		return nil
	}
	if err := safefile.Write(path, doc, safefile.Options{Mode: safefile.RulesMode}); err != nil {
		return fmt.Errorf("write %s: %w", File, err)
	}
	if k.Notify != nil {
		msg := i18n.M("verification.proposed", "commands", strings.Join(commands, ", "), "file", File)
		if err := k.Notify(ctx, workspace, msg); err != nil && k.Log != nil {
			k.Log.Warn("verification: the Administrators were not told of the proposal", "workspace", workspace, "err", err)
		}
	}
	return nil
}
