package vault

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mestre-ia/mestre/internal/logging"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/store/guild"
	"github.com/mestre-ia/mestre/internal/testkit/guildtpl"
)

// deletableKeys is memKeys that can forget the key.
type deletableKeys struct{ memKeys }

func (d *deletableKeys) Delete(context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.data = nil
	return nil
}

func copierOf(t *testing.T, keys KeyStore) Copier {
	t.Helper()
	return Copier{Keys: keys, EnvelopePath: filepath.Join(t.TempDir(), "cofre-envelope.bin")}
}

// The copy of a Tower is the key ring and the envelope of the Citadel, byte for
// byte; the machine that took it holds it, and dropping erases both.
func TestTheCopyOfTheVaultMovesTheKeyAndTheEnvelopeAndDropErasesBoth(t *testing.T) {
	t.Parallel()
	f := newFixture(t, &memKeys{})
	v := f.create(t)
	put(t, v, "github_app_qa", TypeGitHubAppKey, "-----BEGIN RSA PRIVATE KEY-----\nabc\n-----END RSA PRIVATE KEY-----", nil)
	src := Copier{Keys: f.cfg.Keys, EnvelopePath: f.cfg.EnvelopePath}
	cp, err := src.Export(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(cp.Keyring) == 0 || len(cp.Envelope) == 0 {
		t.Fatalf("export = %d bytes of ring, %d of envelope", len(cp.Keyring), len(cp.Envelope))
	}

	dst := copierOf(t, &deletableKeys{})
	ctx := context.Background()
	if dst.Holds(ctx) {
		t.Fatal("a fresh machine holds the vault")
	}
	if err := dst.Import(ctx, cp); err != nil {
		t.Fatal(err)
	}
	if !dst.Holds(ctx) {
		t.Fatal("the machine does not hold the vault after Import")
	}
	got, err := dst.Export(ctx)
	if err != nil || !bytes.Equal(got.Keyring, cp.Keyring) || !bytes.Equal(got.Envelope, cp.Envelope) {
		t.Fatalf("the copy is not the same: %v", err)
	}
	// The envelope on the Tower is the one that opens with the recovery key.
	check := &memKeys{}
	if err := RestoreKey(ctx, got.Envelope, f.rk, check); err != nil {
		t.Fatalf("the envelope of the copy does not open with the recovery key: %v", err)
	}

	if err := dst.Drop(ctx); err != nil {
		t.Fatal(err)
	}
	if dst.Holds(ctx) {
		t.Error("the machine still holds the vault after Drop")
	}
	if _, err := os.Stat(dst.EnvelopePath); err == nil {
		t.Error("the envelope is still there")
	}
	if err := dst.Drop(ctx); err != nil { // dropping again is fine
		t.Errorf("second Drop = %v", err)
	}
}

func TestImportRefusesACopyThatDoesNotAddUp(t *testing.T) {
	t.Parallel()
	f := newFixture(t, &memKeys{})
	f.create(t)
	cp, err := (Copier{Keys: f.cfg.Keys, EnvelopePath: f.cfg.EnvelopePath}).Export(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for name, bad := range map[string]Copy{
		"a ring that is not one":      {Keyring: []byte("nope"), Envelope: cp.Envelope},
		"an envelope that is not one": {Keyring: cp.Keyring, Envelope: []byte("nope")},
		"no envelope":                 {Keyring: cp.Keyring},
	} {
		keys := &deletableKeys{}
		c := copierOf(t, keys)
		if err := c.Import(ctx, bad); err == nil {
			t.Errorf("%s: Import accepted it", name)
		}
		if c.Holds(ctx) {
			t.Errorf("%s: the machine holds a vault after a refused copy", name)
		}
	}
	// An envelope that was made for other key versions than the ring.
	f2 := newDurableFixture(t, &memKeys{})
	v2 := f2.create(t)
	if err := v2.Rotate(f2.ctx); err != nil {
		t.Fatal(err)
	}
	cp2, _ := (Copier{Keys: f2.cfg.Keys, EnvelopePath: f2.cfg.EnvelopePath}).Export(f2.ctx)
	if err := copierOf(t, &deletableKeys{}).Import(ctx, Copy{Keyring: cp.Keyring, Envelope: cp2.Envelope}); err == nil {
		t.Error("an envelope of other key versions was accepted")
	}
}

func TestADropNeedsAKeyStoreThatCanForget(t *testing.T) {
	t.Parallel()
	c := copierOf(t, &memKeys{})
	if err := c.Drop(context.Background()); err == nil {
		t.Error("Drop worked on a key store that cannot delete")
	}
}

// CA-REPL-05: only the Citadel opens the vault.
func TestTheVaultDoesNotOpenOnAReplicaOfTheDatabase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := guild.Open(ctx, filepath.Join(t.TempDir(), "mestre.db"), store.Options{StmtCache: guildtpl.StmtCache, Replica: true, Synchronous: "OFF"})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rk, err := NewRecoveryKey()
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{DB: db, Keys: &memKeys{}, EnvelopePath: filepath.Join(t.TempDir(), "e.bin"), RecoveryPublicKey: rk.PublicKey(), Masker: logging.NewMasker()}
	if _, err := Create(ctx, cfg); !errors.Is(err, ErrReplica) {
		t.Errorf("Create on a replica = %v, want ErrReplica", err)
	}
	if _, err := Open(ctx, cfg); !errors.Is(err, ErrReplica) {
		t.Errorf("Open on a replica = %v, want ErrReplica", err)
	}
}

// Readers of the key (a status check, a promotion check) run while the key is
// erased: none may keep a handle that makes the erasing fail (on Windows a
// file open for reading could not be removed), and none may survive it.
func TestDropWorksWhileTheKeyIsBeingRead(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	keys := NewFileKeyStore(filepath.Join(t.TempDir(), "cofre.key"))
	c := Copier{Keys: keys, EnvelopePath: filepath.Join(t.TempDir(), "cofre-envelope.bin")}
	if err := keys.Save(ctx, []byte("ring")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.EnvelopePath, []byte("env"), 0o600); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				c.Holds(ctx)
			}
		}
	}()
	err := c.Drop(ctx)
	close(stop)
	<-done
	if err != nil {
		t.Fatalf("Drop while the key is read: %v", err)
	}
	if c.Holds(ctx) {
		t.Error("the machine still holds the vault after Drop")
	}
}
