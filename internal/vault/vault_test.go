package vault

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/logging"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/testkit/guildtpl"
	"github.com/lucasndas/mestre/pkg/clock"
)

// memKeys is an in-memory KeyStore: the OS protection is tested on its own
// (keyfile tests), the vault logic everywhere.
type memKeys struct {
	mu     sync.Mutex
	data   []byte
	onSave func(data []byte) // optional, called with each saved keyring
}

func (m *memKeys) Load(context.Context) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data == nil {
		return nil, ErrNoKey
	}
	return slices.Clone(m.data), nil
}

func (m *memKeys) Save(_ context.Context, d []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data = slices.Clone(d)
	if m.onSave != nil {
		m.onSave(slices.Clone(d))
	}
	return nil
}

type fixture struct {
	ctx    context.Context
	dir    string
	db     *store.DB
	rk     *RecoveryKey
	masker *logging.Masker
	clock  *clock.Fake
	cfg    Config

	mu     sync.Mutex
	alerts []Alert
	uses   []UseEvent
}

// newFixture skips the flushes to disk (synchronous=OFF, envelope not
// flushed): the vault logic does not depend on them, and each costs tens of
// milliseconds on a Windows runner. The durability tests use
// newDurableFixture, with the shipped settings.
func newFixture(t *testing.T, keys KeyStore) *fixture {
	t.Helper()
	return newFixtureWith(t, keys, false)
}

func newDurableFixture(t *testing.T, keys KeyStore) *fixture {
	t.Helper()
	return newFixtureWith(t, keys, true)
}

// guildTemplate is a Guild database already migrated, built once by TestMain
// outside the time of every test (creating and migrating a database file
// costs tens of milliseconds on a Windows runner); each fixture starts from
// a copy. The migrations themselves are tested in internal/store/guild.
var guildTemplate []byte

func TestMain(m *testing.M) {
	if err := buildGuildTemplate(); err != nil {
		fmt.Fprintln(os.Stderr, "vault: guild template:", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func buildGuildTemplate() error {
	dir, err := os.MkdirTemp("", "vault-template")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "mestre.db")
	db, err := guildtpl.Open(context.Background(), path, store.Options{Synchronous: "OFF"})
	if err != nil {
		return err
	}
	if err := db.Close(); err != nil {
		return err
	}
	guildTemplate, err = os.ReadFile(path)
	return err
}

func newFixtureWith(t *testing.T, keys KeyStore, durable bool) *fixture {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mestre.db"), guildTemplate, 0o600); err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC))
	opts := store.Options{Clock: clk, Synchronous: "OFF"}
	if durable {
		opts.Synchronous = ""
	}
	db, err := guildtpl.Open(ctx, filepath.Join(dir, "mestre.db"), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	rk, err := NewRecoveryKey()
	if err != nil {
		t.Fatal(err)
	}
	if keys == nil {
		keys = &memKeys{}
	}
	f := &fixture{ctx: ctx, dir: dir, db: db, rk: rk, masker: logging.NewMasker(), clock: clk}
	f.cfg = Config{
		DB:                db,
		Keys:              keys,
		EnvelopePath:      filepath.Join(dir, "cofre-envelope.bin"),
		RecoveryPublicKey: rk.PublicKey(),
		Masker:            f.masker,
		Clock:             clk,
		OnAlert:           func(a Alert) { f.mu.Lock(); f.alerts = append(f.alerts, a); f.mu.Unlock() },
		OnUse:             func(u UseEvent) { f.mu.Lock(); f.uses = append(f.uses, u); f.mu.Unlock() },
		fastEnvelope:      !durable,
	}
	return f
}

func (f *fixture) create(t *testing.T) *Vault {
	t.Helper()
	v, err := Create(f.ctx, f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func (f *fixture) open(t *testing.T) *Vault {
	t.Helper()
	v, err := Open(f.ctx, f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func (f *fixture) alertList() []Alert {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.alerts)
}

func put(t *testing.T, v *Vault, name string, typ Type, value string, site *SiteMeta) {
	t.Helper()
	if err := v.Put(context.Background(), name, typ, []byte(value), site); err != nil {
		t.Fatal(err)
	}
}

func read(v *Vault, name string) (string, error) {
	var out string
	err := v.Use(context.Background(), name, "test", func(b []byte) error { out = string(b); return nil })
	return out, err
}

func mustRead(t *testing.T, v *Vault, name, want string) {
	t.Helper()
	got, err := read(v, name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	if got != want {
		t.Fatalf("read %s: got a different value", name)
	}
}

var sampleSite = &SiteMeta{OwnerUser: "ana", Classes: []string{"qa"}, Domains: []string{"loja.example"}, Kind: KindAppPassword}

// samples are realistic values of each kind of secret.
var samples = []struct {
	name  string
	typ   Type
	value string
	site  *SiteMeta
}{
	{BotTokenSecret, TypeTelegramBotToken, "7012345678:AAH9xk2Lq0ZpW8vYtR3uNc5mBj7eDf1gKo4", nil},
	{"github_app_qa", TypeGitHubAppKey, "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEAz9Qe\n-----END RSA PRIVATE KEY-----", nil},
	{"github_user_ana", TypeGitHubUserToken, `{"access_token":"ghu_R8tY2pQ9wE4rT6yU1iO3pA5sD7fG9hJ0kL","refresh_token":"ghr_Q1w2E3r4T5y6U7i8O9p0AsDfGhJkLzXcVb"}`, nil},
	{"provider_claude_api", TypeProviderAPIKey, "sk-ant-api03-Zx8Cv7Bn6Mm5Ll4Kk3Jj2Hh1", nil},
	{"site_loja", TypeSiteCredential, `{"url":"https://loja.example/login","user":"qa@x","password":"Abc123!senha-longa"}`, sampleSite},
	{"operator_capture", TypeOperatorCaptureKey, "c2VjcmV0LWNhcHR1cmUta2V5LTMyLWJ5dGVzLWxvbmc=", nil},
	{"guild_ca", TypeCAKey, "-----BEGIN EC PRIVATE KEY-----\nMHcCAQEEIJx2Guild\n-----END EC PRIVATE KEY-----", nil},
}

func putSamples(t *testing.T, v *Vault) {
	t.Helper()
	for _, s := range samples {
		put(t, v, s.name, s.typ, s.value, s.site)
	}
}

// CA-COFRE-01
func TestStoredValueAppearsInNoColumn(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	v := f.create(t)
	putSamples(t, v)

	rows, err := f.db.Reader().QueryContext(f.ctx, `SELECT * FROM secrets`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	n := 0
	for rows.Next() {
		n++
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		for i, c := range vals {
			cell := []byte(fmt.Sprint(c))
			if b, ok := c.([]byte); ok {
				cell = b
			}
			for _, s := range samples {
				if bytes.Contains(cell, []byte(s.value)) {
					t.Errorf("column %s holds the value of %s", cols[i], s.name)
				}
			}
		}
	}
	if n != len(samples) {
		t.Fatalf("rows = %d, want %d", n, len(samples))
	}

	// Nor anywhere in the database files (main file and WAL).
	for _, name := range []string{"mestre.db", "mestre.db-wal"} {
		raw, err := os.ReadFile(filepath.Join(f.dir, name))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		for _, s := range samples {
			if bytes.Contains(raw, []byte(s.value)) {
				t.Errorf("%s holds the value of %s in clear", name, s.name)
			}
		}
	}

	for _, s := range samples {
		mustRead(t, v, s.name, s.value)
	}
}

// CA-COFRE-03
func TestCiphertextCopiedToAnotherRowFailsVerification(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"ciphertext and nonce copied": `UPDATE secrets SET ciphertext = (SELECT ciphertext FROM secrets WHERE name = 'a'),
			nonce = (SELECT nonce FROM secrets WHERE name = 'a') WHERE name = 'b'`,
		"row renamed":                  `UPDATE secrets SET name = 'b2' WHERE name = 'b'`,
		"type changed":                 `UPDATE secrets SET type = 'ca_key' WHERE name = 'b'`,
		"key version changed":          `UPDATE secrets SET key_version = 2 WHERE name = 'b'`,
		"ciphertext without its nonce": `UPDATE secrets SET ciphertext = (SELECT ciphertext FROM secrets WHERE name = 'a') WHERE name = 'b'`,
	}
	for name, tamper := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, nil)
			v := f.create(t)
			put(t, v, "a", TypeProviderAPIKey, "sk-first-value-0000000000", nil)
			put(t, v, "b", TypeProviderAPIKey, "sk-second-value-111111111", nil)
			if _, err := f.db.Writer().ExecContext(f.ctx, tamper); err != nil {
				t.Fatal(err)
			}
			target := "b"
			if strings.Contains(tamper, "'b2'") {
				target = "b2"
			}
			_, err := read(v, target)
			if !errors.Is(err, ErrVerification) {
				t.Fatalf("read tampered row: err = %v, want ErrVerification", err)
			}
			for _, val := range []string{"sk-first-value-0000000000", "sk-second-value-111111111"} {
				if strings.Contains(err.Error(), val) {
					t.Fatal("the error carries a secret value")
				}
			}
			mustRead(t, v, "a", "sk-first-value-0000000000")

			// One altered row does not close the vault on restart.
			if err := f.open(t).Err(); err != nil {
				t.Fatalf("reopen with one altered row: %v", err)
			}
		})
	}
}

// CA-COFRE-04
func TestGuardianReplicaHoldsOnlyTheListedFields(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	v := f.create(t)
	putSamples(t, v)
	in := ReplicaInput{
		Version: 7,
		Users: []ReplicaUser{{
			ID: "u1", Name: "Ana", Login: "ana", Level: "dono", Workspaces: []string{"loja"},
			TelegramID: 4242, PasswordHash: "$argon2id$v=19$pw", PINHash: "$argon2id$v=19$pin", Active: true,
		}},
		Locked:       true,
		LastUpdateID: 991,
	}
	wantTop := []string{"bot_token", "last_update_id", "locked", "users", "version"}
	wantUser := []string{"active", "id", "level", "login", "name", "password_hash", "pin_hash", "telegram_id", "workspaces"}
	botToken := samples[0].value

	for _, excluded := range []bool{false, true} {
		in.Excluded = excluded
		r, err := v.BuildGuardianReplica(f.ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]json.RawMessage
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		top := wantTop
		if excluded {
			top = wantTop[1:]
		}
		if got := keys(doc); !slices.Equal(got, top) {
			t.Errorf("excluded=%v: fields %v, want %v", excluded, got, top)
		}
		var users []map[string]json.RawMessage
		if err := json.Unmarshal(doc["users"], &users); err != nil || len(users) != 1 {
			t.Fatalf("users: %v (%d)", err, len(users))
		}
		if got := keys(users[0]); !slices.Equal(got, wantUser) {
			t.Errorf("user fields %v, want %v", got, wantUser)
		}
		for _, s := range samples[1:] {
			if bytes.Contains(raw, []byte(s.value)) || bytes.Contains(raw, []byte(s.name)) {
				t.Errorf("excluded=%v: replica carries %s", excluded, s.name)
			}
		}
		if hasToken := bytes.Contains(raw, []byte(botToken)); hasToken == excluded {
			t.Errorf("excluded=%v: bot token present = %v", excluded, hasToken)
		}
		if strings.Contains(fmt.Sprintf("%v %+v", r, r), botToken) {
			t.Error("printing the replica shows the bot token")
		}
	}
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// CA-COFRE-06
func TestInterruptedKeyRotationKeepsEverySecretReadable(t *testing.T) {
	t.Parallel()
	steps := []struct {
		step        string
		wantVersion uint32 // key_version of the rows after the restart
	}{
		{stepKeyFileStaged, 1},
		{stepEnvelopeStaged, 1},
		{stepDBPartial, 1},
		{stepDBCommitted, 2},
		{stepKeyFileFinal, 2},
		{stepEnvelopeFinal, 2},
	}
	for _, tc := range steps {
		t.Run(tc.step, func(t *testing.T) {
			t.Parallel()
			f := newDurableFixture(t, nil) // a rotation needs a database that flushes
			v := f.create(t)
			putSamples(t, v)
			crash := errors.New("simulated crash")
			v.failAfter = func(s string) error {
				if s == tc.step {
					return crash
				}
				return nil
			}
			if err := v.Rotate(f.ctx); !errors.Is(err, crash) {
				t.Fatalf("Rotate = %v, want the simulated crash", err)
			}

			// Restart: a new engine opens the same database and key.
			v2 := f.open(t)
			if err := v2.Err(); err != nil {
				t.Fatalf("vault closed after restart: %v", err)
			}
			for _, s := range samples {
				mustRead(t, v2, s.name, s.value)
			}
			if got, _ := v2.versionsInUse(f.ctx); !slices.Equal(got, []uint32{tc.wantVersion}) {
				t.Fatalf("key versions in use = %v, want [%d]", got, tc.wantVersion)
			}
			if got := v2.ring.versions(); !slices.Equal(got, []uint32{tc.wantVersion}) {
				t.Errorf("keys kept after restart = %v, want [%d]", got, tc.wantVersion)
			}
			// And on disk, which is what the Towers copy: the key file and the
			// envelope hold only that key, not the one the rotation discarded.
			assertOnlyKeyOnDisk(t, f, tc.wantVersion)
			// The envelope on disk opens the same secrets with the recovery key.
			assertEnvelopeOpens(t, f)

			// And the next rotation completes.
			if err := v2.Rotate(f.ctx); err != nil {
				t.Fatal(err)
			}
			for _, s := range samples {
				mustRead(t, v2, s.name, s.value)
			}
		})
	}
}

// assertOnlyKeyOnDisk reads the key file and the header of the envelope as
// they are on disk, and wants the version of the key to be the only one in
// both (CA-COFRE-06): the memory of the engine being right does not make the
// copies that travel to the Towers right.
func assertOnlyKeyOnDisk(t *testing.T, f *fixture, version uint32) {
	t.Helper()
	data, err := f.cfg.Keys.Load(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	ring, err := parseKeyring(data)
	zero(data)
	if err != nil {
		t.Fatal(err)
	}
	defer ring.destroy()
	if got := ring.versions(); !slices.Equal(got, []uint32{version}) {
		t.Errorf("key file holds the versions %v, want [%d]", got, version)
	}
	env, err := os.ReadFile(f.cfg.EnvelopePath)
	if err != nil {
		t.Fatal(err)
	}
	info, err := parseEnvelopeHeader(env)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(info.versions, []uint32{version}) {
		t.Errorf("envelope holds the versions %v, want [%d]", info.versions, version)
	}
}

// assertEnvelopeOpens restores the key from the envelope into a fresh key
// store, as a restore on a new machine would, and reads every sample.
func assertEnvelopeOpens(t *testing.T, f *fixture) {
	t.Helper()
	env, err := os.ReadFile(f.cfg.EnvelopePath)
	if err != nil {
		t.Fatal(err)
	}
	fresh := &memKeys{}
	if err := RestoreKey(f.ctx, env, f.rk, fresh); err != nil {
		t.Fatalf("restore from envelope: %v", err)
	}
	cfg := f.cfg
	cfg.Keys = fresh
	cfg.OnAlert = func(a Alert) { t.Errorf("alert after restore: %v", a.Err) }
	rv, err := Open(f.ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range samples {
		mustRead(t, rv, s.name, s.value)
	}
}

func TestRotationReencryptsWithANewKey(t *testing.T) {
	t.Parallel()
	f := newDurableFixture(t, nil)
	v := f.create(t)
	putSamples(t, v)
	var before []byte
	_ = f.db.Reader().QueryRowContext(f.ctx, `SELECT ciphertext FROM secrets WHERE name = ?`, samples[0].name).Scan(&before)
	if err := v.Rotate(f.ctx); err != nil {
		t.Fatal(err)
	}
	var after []byte
	var ver int
	_ = f.db.Reader().QueryRowContext(f.ctx, `SELECT ciphertext, key_version FROM secrets WHERE name = ?`, samples[0].name).Scan(&after, &ver)
	if ver != 2 || bytes.Equal(before, after) {
		t.Fatalf("after rotation: key_version %d, ciphertext changed %v", ver, !bytes.Equal(before, after))
	}
	for _, s := range samples {
		mustRead(t, v, s.name, s.value)
	}
	assertEnvelopeOpens(t, f)
	if got := f.open(t).ring.versions(); !slices.Equal(got, []uint32{2}) {
		t.Errorf("key store after rotation holds %v, want [2]", got)
	}
}

// The rows move to the new key in a durable commit (synchronous=FULL) that
// happens before the key store and the envelope drop the old key: a power
// loss after the drop cannot bring back rows that need it.
func TestRotationCommitIsDurableBeforeTheOldKeyIsDropped(t *testing.T) {
	t.Parallel()
	keys := &memKeys{}
	f := newDurableFixture(t, keys)
	v := f.create(t)
	putSamples(t, v)

	var (
		mu     sync.Mutex
		events []string
	)
	record := func(e string) { mu.Lock(); events = append(events, e); mu.Unlock() }
	keys.onSave = func(data []byte) {
		ring, err := parseKeyring(data)
		if err != nil {
			t.Errorf("saved keyring: %v", err)
			return
		}
		record(fmt.Sprintf("keys %v", ring.versions()))
		ring.destroy()
	}
	v.durableHook = func(ctx context.Context, fn func(*sql.Tx) error) error {
		err := f.db.WriteDurable(ctx, func(tx *sql.Tx) error {
			var level int
			if err := tx.QueryRowContext(ctx, `PRAGMA synchronous`).Scan(&level); err != nil {
				return err
			}
			record(fmt.Sprintf("transaction synchronous=%d", level))
			return fn(tx)
		})
		if err == nil {
			record("durable commit")
		}
		return err
	}
	if err := v.Rotate(f.ctx); err != nil {
		t.Fatal(err)
	}
	want := []string{"keys [1 2]", "transaction synchronous=2", "durable commit", "keys [2]"}
	if !slices.Equal(events, want) {
		t.Fatalf("events = %q, want %q", events, want)
	}
	for _, s := range samples {
		mustRead(t, v, s.name, s.value)
	}
}

// CA-COFRE-06
// A database opened without flushes (Synchronous OFF) is refused before any key
// is touched: the guard of durability is not skipped there.
func TestRotationRefusesADatabaseThatDoesNotFlush(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	v := f.create(t)
	putSamples(t, v)
	before, err := f.cfg.Keys.Load(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Rotate(f.ctx); !errors.Is(err, ErrNotDurable) {
		t.Fatalf("Rotate on a database without flushes = %v, want ErrNotDurable", err)
	}
	after, _ := f.cfg.Keys.Load(f.ctx)
	if !bytes.Equal(before, after) {
		t.Error("the key store changed")
	}
	if got, _ := v.versionsInUse(f.ctx); !slices.Equal(got, []uint32{1}) {
		t.Fatalf("key versions in use = %v, want [1]", got)
	}
	for _, s := range samples {
		mustRead(t, v, s.name, s.value)
	}
}

// A commit that would not be synced is refused, and nothing changes.
func TestRotationRefusesACommitThatIsNotDurable(t *testing.T) {
	t.Parallel()
	f := newDurableFixture(t, nil)
	v := f.create(t)
	putSamples(t, v)
	v.durableHook = func(ctx context.Context, fn func(*sql.Tx) error) error {
		tx, err := f.db.Writer().BeginTx(ctx, nil) // synchronous=NORMAL
		if err != nil {
			return err
		}
		defer tx.Rollback() //nolint:errcheck // no-op after Commit
		if err := fn(tx); err != nil {
			return err
		}
		return tx.Commit()
	}
	if err := v.Rotate(f.ctx); err == nil || !strings.Contains(err.Error(), "durable") {
		t.Fatalf("Rotate = %v, want a refusal of the non-durable commit", err)
	}
	if got, _ := v.versionsInUse(f.ctx); !slices.Equal(got, []uint32{1}) {
		t.Fatalf("key versions in use = %v, want [1]", got)
	}
	v2 := f.open(t)
	for _, s := range samples {
		mustRead(t, v2, s.name, s.value)
	}
}

func TestWrongOrMissingKeyClosesTheVaultWithAGraveAlert(t *testing.T) {
	t.Parallel()
	t.Run("wrong key", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, nil)
		v := f.create(t)
		put(t, v, "a", TypeProviderAPIKey, "sk-value-aaaaaaaaaaaaaaaa", nil)
		other, _ := newRandomKey(1)
		_ = f.cfg.Keys.Save(f.ctx, (&keyring{keys: []*dataKey{other}}).marshal())
		assertClosed(t, f, f.open(t), errWrongKey)
	})
	t.Run("missing key", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, nil)
		assertClosed(t, f, f.open(t), ErrClosed)
	})
}

func assertClosed(t *testing.T, f *fixture, v *Vault, reason error) {
	t.Helper()
	if err := v.Err(); !errors.Is(err, ErrClosed) || !errors.Is(err, reason) {
		t.Fatalf("Err = %v, want closed because of %v", err, reason)
	}
	if err := v.Put(f.ctx, "x", TypeProviderAPIKey, []byte("sk-new-value-bbbbbbbbbbbb"), nil); !errors.Is(err, ErrClosed) {
		t.Errorf("Put on a closed vault: %v", err)
	}
	if _, err := read(v, "a"); !errors.Is(err, ErrClosed) {
		t.Errorf("Use on a closed vault: %v", err)
	}
	if err := v.Rotate(f.ctx); !errors.Is(err, ErrClosed) {
		t.Errorf("Rotate on a closed vault: %v", err)
	}
	alerts := f.alertList()
	if len(alerts) != 1 || alerts[0].Severity != SeverityGrave || !errors.Is(alerts[0].Err, ErrClosed) {
		t.Errorf("alerts = %+v, want one grave alert", alerts)
	}
}

// Vault-level part of CA-COFRE-07: the package API never returns a value;
// the panel routes (M6) and CLI commands (M3) must build on List and Info.
func TestMetadataNeverCarriesTheValue(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	v := f.create(t)
	putSamples(t, v)
	list, err := v.List(f.ctx)
	if err != nil || len(list) != len(samples) {
		t.Fatalf("List = %d, %v", len(list), err)
	}
	raw, _ := json.Marshal(list)
	for _, s := range samples {
		if bytes.Contains(raw, []byte(s.value)) {
			t.Errorf("metadata carries the value of %s", s.name)
		}
	}
	var docs []map[string]json.RawMessage
	_ = json.Unmarshal(raw, &docs)
	allowed := []string{"created_at", "fingerprint", "last_used_at", "name", "site", "type", "updated_at"}
	for _, d := range docs {
		for k := range d {
			if !slices.Contains(allowed, k) {
				t.Errorf("metadata field %q is not in the allowed list", k)
			}
		}
	}
	info, err := v.Info(f.ctx, "site_loja")
	if err != nil || info.Site == nil || info.Site.OwnerUser != "ana" || !slices.Equal(info.Site.Domains, []string{"loja.example"}) {
		t.Fatalf("Info(site_loja) = %+v, %v", info, err)
	}
	if len(info.Fingerprint) != FingerprintLen {
		t.Errorf("fingerprint %q has %d characters", info.Fingerprint, len(info.Fingerprint))
	}
	if _, err := v.Info(f.ctx, "nope"); !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), `"nope"`) {
		t.Errorf("Info(nope) = %v, want not found naming the secret", err)
	}
}

func TestUseRegistersTheValueAndRecordsThePurpose(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	v := f.create(t)
	const val = "plain words password"
	put(t, v, "p", TypeProviderAPIKey, val, nil)
	f.clock.Advance(time.Hour)

	if err := v.Use(f.ctx, "p", "  ", func([]byte) error { return nil }); err == nil {
		t.Fatal("Use without a purpose succeeded")
	}
	var held []byte
	if err := v.Use(f.ctx, "p", "start character qa-1", func(b []byte) error { held = b; return nil }); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(held, make([]byte, len(val))) {
		t.Error("the buffer handed to fn was not wiped after fn returned")
	}
	if got := f.masker.Mask("log: " + val); strings.Contains(got, val) {
		t.Errorf("value not registered in the masker: %q", got)
	}
	if len(f.uses) != 1 || f.uses[0].Name != "p" || f.uses[0].Purpose != "start character qa-1" {
		t.Errorf("uses = %+v", f.uses)
	}
	info, _ := v.Info(f.ctx, "p")
	if info.LastUsedAt == nil || !info.LastUsedAt.Equal(f.clock.Now()) {
		t.Errorf("last_used_at = %v, want %v", info.LastUsedAt, f.clock.Now())
	}
	if _, err := read(v, "missing"); !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), `"missing"`) {
		t.Errorf("read missing: %v", err)
	}
}

// Each sensitive part of a value is masked alone: the password of a site
// credential, the access and refresh tokens of a GitHub user token and each
// line of a PEM key, logged apart from the rest.
func TestUseMasksEachPartOfCompositeValues(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	v := f.create(t)
	putSamples(t, v)
	pem := "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEAz9Qe\nq8Rt7Ys3Lp0Wd5Xc2Vb\n-----END RSA PRIVATE KEY-----\n"
	put(t, v, "app_dev", TypeGitHubAppKey, pem, nil)
	for _, name := range []string{"site_loja", "github_user_ana", "app_dev"} {
		if _, err := read(v, name); err != nil {
			t.Fatal(err)
		}
	}
	for _, part := range []string{
		"Abc123!senha-longa",                     // site_credential password
		"ghu_R8tY2pQ9wE4rT6yU1iO3pA5sD7fG9hJ0kL", // access token
		"ghr_Q1w2E3r4T5y6U7i8O9p0AsDfGhJkLzXcVb", // refresh token
		"MIIEowIBAAKCAQEAz9Qe",                   // PEM lines
		"q8Rt7Ys3Lp0Wd5Xc2Vb",
	} {
		line := "login with " + part + " failed"
		if got := f.masker.Mask(line); strings.Contains(got, part) || !strings.Contains(got, logging.Redacted) {
			t.Errorf("%q is not masked alone: %q", part, got)
		}
	}
	// The armor lines (BEGIN is a known format of the masker anyway), the URL
	// and the user are not secret.
	for _, plain := range []string{"-----END RSA PRIVATE KEY-----", "https://loja.example/login", "qa@x"} {
		if got := f.masker.Mask("see " + plain); !strings.Contains(got, plain) {
			t.Errorf("%q was masked: %q", plain, got)
		}
	}
}

func TestPutValidatesCompositeValues(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	v := f.create(t)
	const secret = "Zx9-never-in-errors"
	bad := []struct {
		typ   Type
		value string
	}{
		{TypeSiteCredential, secret},
		{TypeSiteCredential, `{"url":"https://a.example","user":"u"}`},
		{TypeSiteCredential, `{"url":"https://a.example","password":"` + secret + `","pin":"1234"}`},
		{TypeSiteCredential, `{"password":"` + secret + `"}`},
		{TypeGitHubUserToken, "ghu_" + secret},
		{TypeGitHubUserToken, `{"refresh_token":"` + secret + `"}`},
		{TypeGitHubUserToken, `{"access_token":"` + secret + `"} {}`},
	}
	for _, b := range bad {
		var site *SiteMeta
		if b.typ == TypeSiteCredential {
			site = sampleSite
		}
		err := v.Put(f.ctx, "x", b.typ, []byte(b.value), site)
		if err == nil {
			t.Errorf("Put(%s, %q) succeeded", b.typ, b.value)
		} else if strings.Contains(err.Error(), secret) {
			t.Errorf("error carries the value: %v", err)
		}
	}
	put(t, v, "only_access", TypeGitHubUserToken, `{"access_token":"ghu_only-access-token-0000"}`, nil)
	put(t, v, "no_user", TypeSiteCredential, `{"url":"https://api.example","password":"k-123456"}`, sampleSite)
}

func TestPutValidatesTypeAndSiteMetadata(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	v := f.create(t)
	const val = "sk-never-in-errors-123456"
	bad := []struct {
		name string
		typ  Type
		site *SiteMeta
	}{
		{"x", "unknown", nil},
		{"x", TypeSiteCredential, nil},
		{"x", TypeSiteCredential, &SiteMeta{OwnerUser: "ana", Classes: []string{"qa"}, Domains: []string{"a"}, Kind: "outro"}},
		{"x", TypeSiteCredential, &SiteMeta{Classes: []string{"qa"}, Domains: []string{"a"}, Kind: KindToken}},
		{"x", TypeProviderAPIKey, sampleSite},
		{"", TypeProviderAPIKey, nil},
	}
	for _, b := range bad {
		err := v.Put(f.ctx, b.name, b.typ, []byte(val), b.site)
		if err == nil {
			t.Errorf("Put(%q, %s, %+v) succeeded", b.name, b.typ, b.site)
		} else if strings.Contains(err.Error(), val) {
			t.Errorf("error carries the value: %v", err)
		}
	}
}

func TestCreateRefusesToReplaceAnExistingKey(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	f.create(t)
	if _, err := Create(f.ctx, f.cfg); err == nil {
		t.Fatal("second Create succeeded")
	}
}

// A lost key with secrets still stored is not a new vault: a new key would
// never open them.
func TestCreateRefusesWhenSecretsRemainWithoutAKey(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	v := f.create(t)
	put(t, v, "a", TypeProviderAPIKey, "sk-value-aaaaaaaaaaaaaaaa", nil)
	cfg := f.cfg
	cfg.Keys = &memKeys{} // the key file is gone
	if _, err := Create(f.ctx, cfg); err == nil || !strings.Contains(err.Error(), "restore") {
		t.Fatalf("Create with stored secrets and no key = %v, want a refusal pointing to the restore", err)
	}
	if b, _ := cfg.Keys.Load(f.ctx); b != nil {
		t.Error("Create saved a key although it refused")
	}
}

func TestRecoveryKeyRoundTripAndRedaction(t *testing.T) {
	t.Parallel()
	rk, err := NewRecoveryKey()
	if err != nil {
		t.Fatal(err)
	}
	text := rk.Reveal()
	groups := strings.Split(text, "-")
	if len(groups) != 13 || len(strings.ReplaceAll(text, "-", "")) != RecoveryKeyLen {
		t.Fatalf("Reveal = %d groups, %d characters", len(groups), len(strings.ReplaceAll(text, "-", "")))
	}
	back, err := ParseRecoveryKey(strings.ToLower(strings.Join(groups, " ")))
	if err != nil || !bytes.Equal(back.PublicKey(), rk.PublicKey()) {
		t.Fatalf("ParseRecoveryKey: %v", err)
	}
	for _, s := range []string{
		fmt.Sprint(rk), fmt.Sprintf("%v %+v %#v %s", rk, rk, rk, rk),
		fmt.Sprint(*rk), fmt.Sprintf("%v %+v %#v %s", *rk, *rk, *rk, *rk),
	} {
		if strings.Contains(s, groups[0]+groups[1]) || strings.Contains(s, groups[0]+"-") {
			t.Errorf("printing the recovery key shows it: %q", s)
		}
	}
	if _, err := ParseRecoveryKey(text[:len(text)-1]); err == nil {
		t.Error("truncated key accepted")
	}
}

func TestEnvelopeRefusesAWrongRecoveryKey(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	f.create(t)
	env, err := os.ReadFile(f.cfg.EnvelopePath)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := NewRecoveryKey()
	if err := RestoreKey(f.ctx, env, other, &memKeys{}); !errors.Is(err, ErrRecoveryKeyMismatch) {
		t.Errorf("wrong recovery key: %v", err)
	}
	env[len(env)-1] ^= 1
	if err := RestoreKey(f.ctx, env, f.rk, &memKeys{}); err == nil {
		t.Error("altered envelope opened")
	}
}

// Vault-level part of CA-COFRE-21: the release decision from metadata
// alone. The Tower request over the protocol and its audit close in M5
// (ordem-de-implementacao.md, "Critérios em marcos posteriores").
func TestSiteCredentialReleasedOnlyToListedClasses(t *testing.T) {
	t.Parallel()
	cred := Info{Name: "site_loja", Type: TypeSiteCredential, Site: sampleSite}
	cases := []struct {
		class       string
		classCreds  []string
		cred        Info
		wantAllowed bool
	}{
		{"qa", []string{"site_loja"}, cred, true},
		{"dev", []string{"site_loja"}, cred, false}, // class not listed by the credential
		{"qa", nil, cred, false},                    // class does not list the credential
		{"qa", []string{"github_app_qa"}, Info{Name: "github_app_qa", Type: TypeGitHubAppKey}, false},
	}
	for _, c := range cases {
		err := SiteCredentialAllowed(c.cred, c.class, c.classCreds)
		if (err == nil) != c.wantAllowed || (err != nil && !errors.Is(err, ErrNotReleased)) {
			t.Errorf("class %s, creds %v, %s: err = %v, want allowed %v", c.class, c.classCreds, c.cred.Name, err, c.wantAllowed)
		}
	}
}

// The envelope is replaced in place, atomically and durably (MoveFileEx with
// write-through on Windows), leaving no temporary file behind.
func TestWriteFileAtomicReplacesTheFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "cofre-envelope.bin")
	for _, content := range []string{"first", "second, longer than the first"} {
		if err := writeFileAtomic(path, []byte(content)); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != content {
			t.Fatalf("content = %q, %v; want %q", got, err, content)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("folder holds %d entries, %v; want only the envelope", len(entries), err)
	}
}
