package app

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/mestre-ia/mestre/internal/guild"
	"github.com/mestre-ia/mestre/internal/store"
	guilddb "github.com/mestre-ia/mestre/internal/store/guild"
	"github.com/mestre-ia/mestre/internal/store/machine"
	"github.com/mestre-ia/mestre/internal/users"
	"github.com/mestre-ia/mestre/internal/vault"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// SetupCodeMain is `mestre setup-code [--instance <inst>] [--dir <pasta>]`: it makes
// a new code of setup of the Owner, voiding the one before, for a Guild that has
// no user yet (docs/painel/login.md#assistente-do-dono-primeira-vez). Only root or
// an administrator, on the machine of the Citadel, and never from an agent. It
// returns the exit code.
func SetupCodeMain(ctx context.Context, args []string, stdout, stderr io.Writer, lookupEnv func(string) (string, bool)) int {
	return SetupCodeWith(ctx, args, stdout, stderr, lookupEnv, isElevated)
}

// SetupCodeWith is SetupCodeMain with the test of the privilege given: root on
// Linux, elevated on Windows. The tests give theirs.
func SetupCodeWith(ctx context.Context, args []string, stdout, stderr io.Writer, lookupEnv func(string) (string, bool), elevated func() bool) int {
	if _, agent := lookupEnv("MESTRE_AGENT_ID"); agent {
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
		fmt.Fprintln(stderr, "error: mestre setup-code needs root (Linux) or an administrator (Windows)")
		return 3
	}
	paths := DirPaths(*dir)
	if *dir == "" {
		var err error
		if paths, err = DefaultPaths(*instance); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 2
		}
	}
	code, err := newSetupCode(ctx, paths)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
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
	id, _, err := guild.NewMember(mdb, vault.NewFileKeyStore(paths.engineKey()), clk).TLS(ctx)
	if err != nil {
		return "", fmt.Errorf("this instance is in no Guild: %w", err)
	}
	if !id.IsCitadel() {
		return "", fmt.Errorf("this machine is not the Citadel: the code of setup is made where the Guild was created")
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
