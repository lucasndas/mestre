package main

import (
	"context"
	"fmt"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/github"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// seedAdmin gives the screens of the administration the states a fresh Guild never has, for
// who looks at them in a browser (the `-rich` flag): the Apps ready, installed, waiting for
// their creation and with the link expired, two proposals of class waiting for the approval (one
// with a problem), the second machine of the Guild online-less but active and a request to join
// the Guild. It is off in the smoke and the journey tests, which count on the fresh Guild
// (one machine, no App).
func seedAdmin(ctx context.Context, db *store.DB, now time.Time) error {
	apps := &github.Registry{DB: db, Clock: clock.Real{}}
	for _, a := range []struct {
		app       github.App
		installed bool
	}{
		{github.App{Name: "lucas-mestre-grao-mestre", Role: github.RoleGrandMaster}, true},
		{github.App{Name: "lucas-mestre-mestre", Role: github.RoleMestre}, true},
		{github.App{Name: "lucas-mestre-qa", Role: github.RoleClass, Class: "qa"}, false},
		{github.App{Name: "lucas-mestre-revisor", Role: github.RoleClass, Class: "revisor"}, false},
	} {
		if _, err := apps.Request(ctx, a.app); err != nil {
			return fmt.Errorf("the App %s: %w", a.app.Name, err)
		}
		if a.installed {
			if err := apps.Created(ctx, a.app.Name, a.app.Name, 1, "cid"); err != nil {
				return err
			}
			if err := apps.Installed(ctx, a.app.Name, 1, []string{"lucas/loja"}); err != nil {
				return err
			}
		}
	}
	if err := apps.SetState(ctx, "lucas-mestre-revisor", classes.AppExpired); err != nil {
		return err
	}
	if err := apps.SetOthers(ctx, 4); err != nil {
		return err
	}

	recs := classes.ClassDB{DB: db, Clock: clock.Real{}}
	files, err := classes.NewFiles([]classes.File{
		{Path: "CLASS.md", Data: []byte("---\nname: testador\ndescription: Escreve e roda os testes da mesa\napp: novo\n---\nEscreva os testes que faltam.\n")},
		{Path: "permissions.yaml", Data: []byte("version: 1\nfiles:\n  read: [\"**\"]\n")},
	})
	if err != nil {
		return err
	}
	if _, _, err := recs.Propose(ctx, "loja", classes.Proposal{Key: classes.ClassKey{Scope: classes.ClassWorkspace, Name: "testador"}, Hash: files.Hash(), Files: files, Origin: classes.OriginRequest, By: "e2e"}); err != nil {
		return fmt.Errorf("the proposal: %w", err)
	}
	broken, err := classes.NewFiles([]classes.File{{Path: "CLASS.md", Data: []byte("---\nname: quebrada\ndescription: Falta o prompt\napp: novo\n---\n")}})
	if err != nil {
		return err
	}
	if _, _, err := recs.Propose(ctx, "loja", classes.Proposal{Key: classes.ClassKey{Scope: classes.ClassWorkspace, Name: "quebrada"}, Hash: broken.Hash(), Files: broken, Origin: classes.OriginDisk, Problems: []string{"falta o prompt"}}); err != nil {
		return fmt.Errorf("the proposal with a problem: %w", err)
	}

	// the second machine is part of the Guild now, with no signal, and another asks to join
	if _, err := db.Writer().ExecContext(ctx, `UPDATE machines SET state = 'active' WHERE machine_id = 'e2e-torre'`); err != nil {
		return fmt.Errorf("the second machine: %w", err)
	}
	if _, err := db.Writer().ExecContext(ctx, `INSERT INTO join_requests (join_id, state, remote_ip, name, request, csr_motor, csr_guard, commitment, nonce_c, fingerprint, code, created_at, expires_at)
		VALUES ('e2e-join', 'pending', '192.168.1.60', 'pc-da-sala', '{}', X'00', X'00', X'00', X'00', zeroblob(32), '482917', ?, ?)`,
		now.UnixMilli(), now.Add(30*time.Minute).UnixMilli()); err != nil {
		return fmt.Errorf("the request to join: %w", err)
	}
	return nil
}
