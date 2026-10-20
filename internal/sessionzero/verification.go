package sessionzero

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"go.yaml.in/yaml/v3"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/safefile"
)

// The commands of verification of a workspace
// (docs/sessao-zero/workspace.md#comandos-de-verificação): the Grand Master
// proposes them in the stage analysis, in .mestre/regras/verificacao.yaml of the
// main clone, and an Administrator or above confirms them. The file is not what
// holds: the last version confirmed is (verification_commands), so a proposal
// that was not confirmed, or a hand edit, changes nothing and the Administrators
// are told.

// VerificationFile is the file of the proposal, relative to the main clone.
const VerificationFile = MestreDir + "/regras/verificacao.yaml"

// VerificationVersion is the version of the format of the file.
const VerificationVersion = 1

// Limits of the file.
const (
	maxVerificationCommands = 20
	maxVerificationCommand  = 1 << 10
	maxVerificationBytes    = 16 << 10
	// DefaultVerificationTimeout is the time of each command when the file says none.
	DefaultVerificationTimeout = 15 * time.Minute
	maxVerificationTimeout     = 2 * time.Hour
)

// Verification is the content of verificacao.yaml: the commands, in order (the
// first one that leaves with a code other than 0 fails the verification), and the
// time limit of each one.
type Verification struct {
	Commands []string
	Timeout  time.Duration
}

type verificationDoc struct {
	Version  int      `yaml:"version"`
	Comandos []string `yaml:"comandos"`
	Timeout  string   `yaml:"timeout,omitempty"`
}

// ErrInvalidVerification wraps every error of an invalid verificacao.yaml.
var ErrInvalidVerification = errors.New("invalid verificacao.yaml")

// ParseVerification reads a verificacao.yaml strictly: version 1, one document, a
// list of commands of one line each, and a time limit.
func ParseVerification(data []byte) (Verification, error) {
	if len(data) > maxVerificationBytes {
		return Verification{}, fmt.Errorf("%w: more than %d KiB", ErrInvalidVerification, maxVerificationBytes>>10)
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var d verificationDoc
	if err := dec.Decode(&d); err != nil {
		return Verification{}, fmt.Errorf("%w: %v", ErrInvalidVerification, err)
	}
	var extra any
	if err := dec.Decode(&extra); err == nil {
		return Verification{}, fmt.Errorf("%w: more than one YAML document", ErrInvalidVerification)
	}
	if d.Version != VerificationVersion {
		return Verification{}, fmt.Errorf("%w: version %d: this binary reads version %d", ErrInvalidVerification, d.Version, VerificationVersion)
	}
	if err := validVerificationCommands(d.Comandos); err != nil {
		return Verification{}, err
	}
	v := Verification{Commands: d.Comandos, Timeout: DefaultVerificationTimeout}
	if d.Timeout != "" {
		t, err := time.ParseDuration(d.Timeout)
		if err != nil || t <= 0 || t > maxVerificationTimeout {
			return Verification{}, fmt.Errorf("%w: timeout %q: a duration up to %s", ErrInvalidVerification, d.Timeout, maxVerificationTimeout)
		}
		v.Timeout = t
	}
	return v, nil
}

func validVerificationCommands(cmds []string) error {
	if len(cmds) == 0 || len(cmds) > maxVerificationCommands {
		return fmt.Errorf("%w: comandos has %d items; from 1 to %d", ErrInvalidVerification, len(cmds), maxVerificationCommands)
	}
	for i, c := range cmds {
		switch {
		case strings.TrimSpace(c) == "" || len(c) > maxVerificationCommand:
			return fmt.Errorf("%w: command %d is empty or longer than %d bytes", ErrInvalidVerification, i+1, maxVerificationCommand)
		case strings.ContainsFunc(c, unicode.IsControl):
			return fmt.Errorf("%w: command %d has a control character: a command is one line", ErrInvalidVerification, i+1)
		}
	}
	return nil
}

// YAML is the file of the verification.
func (v Verification) YAML() []byte {
	timeout := v.Timeout
	if timeout <= 0 {
		timeout = DefaultVerificationTimeout
	}
	b, err := yaml.Marshal(verificationDoc{Version: VerificationVersion, Comandos: v.Commands, Timeout: timeout.String()})
	if err != nil { // a struct of text always encodes
		panic(err)
	}
	return b
}

// VerificationHash is the hash of a document, which an Administrator confirms.
func VerificationHash(doc []byte) string {
	sum := sha256.Sum256(doc)
	return hex.EncodeToString(sum[:])
}

// ErrVerificationChanged: the file is not the one the Administrator saw.
var ErrVerificationChanged = errors.New("o verificacao.yaml mudou desde a versão mostrada")

// ErrVerificationNeedsAdministrator: only an Administrator or above confirms.
var ErrVerificationNeedsAdministrator = errors.New("forbidden: só um Administrador ou acima confirma os comandos de verificação")

// verificationRow is the confirmed version of a workspace.
type verificationRow struct {
	hash, doc, by string
	at            time.Time
	alerted       string
}

func (s Store) verification(ctx context.Context, slug string) (verificationRow, bool, error) {
	var (
		r  verificationRow
		at int64
	)
	err := s.DB.Reader().QueryRowContext(ctx, `SELECT hash, doc, confirmed_by, confirmed_at, alerted_hash FROM verification_commands WHERE workspace = ?`, slug).
		Scan(&r.hash, &r.doc, &r.by, &at, &r.alerted)
	if errors.Is(err, sql.ErrNoRows) {
		return verificationRow{}, false, nil
	}
	if err != nil {
		return verificationRow{}, false, fmt.Errorf("verification %s: %w", slug, err)
	}
	r.at = fromMs(at)
	return r, true, nil
}

func (s Store) confirmVerification(ctx context.Context, slug, hash, doc, by string, now time.Time) error {
	return s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO verification_commands (workspace, hash, doc, confirmed_by, confirmed_at, alerted_hash) VALUES (?, ?, ?, ?, ?, '')
			ON CONFLICT (workspace) DO UPDATE SET hash = excluded.hash, doc = excluded.doc, confirmed_by = excluded.confirmed_by, confirmed_at = excluded.confirmed_at, alerted_hash = ''`,
			slug, hash, doc, by, ms(now))
		return err
	})
}

func (s Store) verificationAlerted(ctx context.Context, slug, hash string) error {
	return s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE verification_commands SET alerted_hash = ? WHERE workspace = ?`, hash, slug)
		return err
	})
}

// readVerificationFile is the file of the main clone; absent or blank is nil.
func readVerificationFile(dir string) ([]byte, error) {
	b, _, err := safefile.Read(filepath.Join(dir, filepath.FromSlash(VerificationFile)), maxVerificationBytes+1)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, nil
	case err != nil:
		return nil, err
	case len(bytes.TrimSpace(b)) == 0:
		return nil, nil
	}
	return b, nil
}

// proposeVerification writes the proposal of the Grand Master in the file of the
// main clone and tells the Administrators it waits for their confirmation. The
// file is born empty and only gets a proposal: one that has content already (the
// proposal of an earlier run, or what people wrote) is left as it is.
func (s *Service) proposeVerification(ctx context.Context, slug, dir string, commands []string) error {
	if len(commands) == 0 {
		return nil
	}
	if err := validVerificationCommands(commands); err != nil {
		return err
	}
	cur, err := readVerificationFile(dir)
	if err != nil {
		return fmt.Errorf("read %s: %w", VerificationFile, err)
	}
	if cur != nil {
		return nil
	}
	doc := Verification{Commands: commands}.YAML()
	if err := safefile.Write(filepath.Join(dir, filepath.FromSlash(VerificationFile)), doc, safefile.Options{}); err != nil {
		return fmt.Errorf("write %s: %w", VerificationFile, err)
	}
	text := "O Grão-Mestre propôs os comandos que provam que o projeto está de pé (" + strings.Join(commands, ", ") + "), em " + VerificationFile +
		". Nada vale antes da confirmação de um Administrador ou acima, pelo painel (versão " + VerificationHash(doc)[:12] + ")."
	// the proposal is written: a failure of the notice does not fail the stage (a new
	// run would find the file and not tell), the panel shows the proposal anyway
	if err := s.o.Users.Notify(ctx, slug, Notice{Kind: "verification_proposed", Text: text}); err != nil {
		s.log.Warn("sessionzero: the Administrators were not told of the proposal of verification", "workspace", slug, "err", err)
	}
	return nil
}

// ConfirmVerification is the "Confirmar" of an Administrator or above: the file of
// the main clone, as the person saw it (hash), becomes the version that holds. A
// file that does not read, or that changed since, is refused.
func (s *Service) ConfirmVerification(ctx context.Context, slug, hash string, by classes.Author) error {
	return s.confirmVerification(ctx, slug, s.o.Workspaces.Layout().Principal(slug), hash, by)
}

func (s *Service) confirmVerification(ctx context.Context, slug, dir, hash string, by classes.Author) error {
	if by.Level != classes.LevelOwner && by.Level != classes.LevelAdmin {
		return fmt.Errorf("%w (%s)", ErrVerificationNeedsAdministrator, by.Level)
	}
	doc, err := readVerificationFile(dir)
	if err != nil {
		return fmt.Errorf("read %s: %w", VerificationFile, err)
	}
	if doc == nil {
		return fmt.Errorf("%w: o arquivo está vazio", ErrInvalidVerification)
	}
	if VerificationHash(doc) != hash {
		return ErrVerificationChanged
	}
	if _, err := ParseVerification(doc); err != nil {
		return err
	}
	return s.store.confirmVerification(ctx, slug, hash, string(doc), by.ID, s.o.Clock.Now())
}

// VerificationCommands are the commands of the last version confirmed, or none
// (false) while no one confirmed any: then nothing is verified and nothing is
// approved alone. A file that differs from the confirmed version, edited outside
// the panel, changes nothing, and the Administrators are told once for each such
// version.
func (s *Service) VerificationCommands(ctx context.Context, slug string) (Verification, bool, error) {
	return s.verificationCommands(ctx, slug, s.o.Workspaces.Layout().Principal(slug))
}

func (s *Service) verificationCommands(ctx context.Context, slug, dir string) (Verification, bool, error) {
	row, ok, err := s.store.verification(ctx, slug)
	if err != nil || !ok {
		return Verification{}, false, err
	}
	v, err := ParseVerification([]byte(row.doc))
	if err != nil { // what was confirmed was valid: a record that is not is a broken database
		return Verification{}, false, err
	}
	if cur, rerr := readVerificationFile(dir); rerr == nil && cur != nil && VerificationHash(cur) != row.hash {
		if h := VerificationHash(cur); row.alerted != h {
			text := "O " + VerificationFile + " foi mudado fora do painel. Continua valendo a última versão confirmada; um Administrador confirma a nova pelo painel (versão " + h[:12] + ")."
			if err := s.o.Users.Notify(ctx, slug, Notice{Kind: "verification_edited", Text: text}); err != nil {
				return v, true, err
			}
			if err := s.store.verificationAlerted(ctx, slug, h); err != nil {
				return v, true, err
			}
		}
	}
	return v, true, nil
}

// VerificationConfirmed says whether the workspace has verification commands
// confirmed (quests.Verifier.Confirmed).
func (s *Service) VerificationConfirmed(ctx context.Context, slug string) (bool, error) {
	_, ok, err := s.VerificationCommands(ctx, slug)
	return ok, err
}
