package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/mestre-ia/mestre/internal/guild"
	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/store"
	guilddb "github.com/mestre-ia/mestre/internal/store/guild"
	"github.com/mestre-ia/mestre/internal/store/machine"
	"github.com/mestre-ia/mestre/internal/users"
	"github.com/mestre-ia/mestre/internal/vault"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// SetupCodeMain is `mestre setup-code [--instance <inst>] [--dir <pasta>]`: it makes
// a new code of setup of the Owner, voiding the one before, for a Guild that has
// no user yet (docs/painel/login.md#assistente-do-senhor-da-guilda-primeira-vez). Only root or
// an administrator, on the machine of the Citadel, and never from an agent. It
// returns the exit code.
func SetupCodeMain(ctx context.Context, args []string, stdout, stderr io.Writer, lookupEnv func(string) (string, bool)) int {
	return SetupCodeWith(ctx, args, stdout, stderr, lookupEnv, isElevated)
}

// SetupCodeWith is SetupCodeMain with the test of the privilege given: root on
// Linux, elevated on Windows. The tests give theirs.
func SetupCodeWith(ctx context.Context, args []string, stdout, stderr io.Writer, lookupEnv func(string) (string, bool), elevated func() bool) int {
	lang := i18n.MachineLanguageFrom(lookupEnv)
	if _, agent := lookupEnv("MESTRE_AGENT_ID"); agent {
		// for an agent, never translated
		fmt.Fprintln(stderr, "error: an agent never makes the code of setup (MESTRE_AGENT_ID is in the environment)")
		return 3
	}
	fs := flag.NewFlagSet("setup-code", flag.ContinueOnError)
	fs.SetOutput(stderr)
	instance := fs.String("instance", DefaultInstance, "the instance of the Mestre")
	dir := fs.String("dir", "", "every folder of the instance under this folder")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintln(stderr, "usage: mestre setup-code [--instance <inst>] [--dir <folder>]")
		return 2
	}
	if !elevated() {
		fmt.Fprintln(stderr, "error: "+i18n.Render(lang, i18n.M("person.needs_root")))
		return 3
	}
	paths := DirPaths(*dir)
	if *dir == "" {
		var err error
		if paths, err = DefaultPaths(*instance); err != nil {
			fmt.Fprintln(stderr, "error:", personText(lang, err))
			return 2
		}
	}
	code, err := newSetupCode(ctx, paths)
	if err != nil {
		fmt.Fprintln(stderr, "error:", personText(lang, err))
		return 1
	}
	fmt.Fprintln(stdout, code)
	return 0
}

// newSetupCode opens the Guild of the instance, which must be the Citadel's, and
// asks the users for a new code.
func newSetupCode(ctx context.Context, paths Paths) (string, error) {
	clk := clock.Real{}
	mdb, err := machine.Open(ctx, paths.machineDB(), store.Options{Clock: clk})
	if err != nil {
		return "", err
	}
	defer mdb.Close()
	member := guild.NewMember(mdb, vault.NewFileKeyStore(paths.engineKey()), clk)
	// the pending key of a renewal under way in the running engine
	member.UsePendingKey(vault.NewFileKeyStore(paths.pendingKey()))
	id, _, err := member.TLS(ctx)
	if err != nil {
		return "", personErr{i18n.M("person.instance_in_no_guild", "detail", err.Error())}
	}
	if !id.IsCitadel() {
		return "", personErr{i18n.M("person.not_citadel")}
	}
	db, err := guilddb.Open(ctx, paths.guildDB(), store.Options{Clock: clk})
	if err != nil {
		return "", err
	}
	defer db.Close()
	svc, err := users.New(users.Options{DB: db, Clock: clk})
	if err != nil {
		return "", err
	}
	code, err := svc.NewSetupCode(ctx)
	if err != nil {
		return "", err
	}
	return code, nil
}

// personErr is an error that is a message of the catalog: the command says it in
// the language of the person (personText); anywhere else it reads as the code and
// its parameters.
type personErr struct{ msg i18n.Msg }

func (e personErr) Error() string { return e.msg.String() }

// personText is the text of an error for a person: a personErr in the language,
// any other error (a detail from below, in English) as it is.
func personText(lang string, err error) string {
	var pe personErr
	if errors.As(err, &pe) {
		return i18n.Render(lang, pe.msg)
	}
	return err.Error()
}
