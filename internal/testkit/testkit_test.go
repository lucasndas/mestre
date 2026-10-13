package testkit_test

import (
	"fmt"
	"io/fs"
	"net/http/httptest"
	"path"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/lucasndas/mestre/internal/testkit"
)

// CA-DUBLES-10
func TestScanFindsTelegramTokenInRecording(t *testing.T) {
	t.Parallel()
	token := "7000000001:" + strings.Repeat("AAbbCCddEE", 3) + "ffGGh"
	fsys := fstest.MapFS{
		"telegram/bot-api-10.3/clean.json": {Data: []byte(`{"body": {"text": "oi"}}`)},
		"telegram/bot-api-10.3/leak.json":  {Data: []byte("{\n  \"token\": \"" + token + "\"\n}")},
	}
	got, err := testkit.ScanSecrets(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].String() != "telegram/bot-api-10.3/leak.json:2: telegram-bot-token" {
		t.Fatalf("findings = %v", got)
	}
}

func TestScanFindsGitHubTokenAndPEM(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"github/v/a.json": {Data: []byte(`"ghs_` + strings.Repeat("x1Y", 12) + `"`)},
		"github/v/b.json": {Data: []byte("-----BEGIN " + "EC PRIVATE KEY-----")},
	}
	got, err := testkit.ScanSecrets(fsys)
	if err != nil || len(got) != 2 {
		t.Fatalf("findings = %v, %v", got, err)
	}
}

// scanCanary is a dot file the embed must carry: without the "all:" prefix
// the embed directive skips files starting with "." or "_", and a leaked token in one
// of them would never be scanned.
const scanCanary = ".scan-canary"

// CA-DUBLES-10
func TestRecordingsEmbedDotFilesSoTheScanSeesThem(t *testing.T) {
	t.Parallel()
	if _, err := fs.Stat(testkit.Recordings(), scanCanary); err != nil {
		t.Fatalf("dot file not embedded (go:embed needs the all: prefix): %v", err)
	}
	var seen bool
	_ = fs.WalkDir(testkit.Recordings(), ".", func(p string, _ fs.DirEntry, _ error) error {
		seen = seen || p == scanCanary
		return nil
	})
	if !seen {
		t.Fatal("the walk the secret scan does skips dot files")
	}
}

// The guard itself: the real recordings never hold secrets.
func TestRecordingsHaveNoSecrets(t *testing.T) {
	t.Parallel()
	got, err := testkit.ScanSecrets(testkit.Recordings())
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range got {
		t.Errorf("secret in internal/testkit/testdata/recordings/%s", f)
	}
}

func TestEveryRecordingHasVersionAndDate(t *testing.T) {
	t.Parallel()
	fsys := testkit.Recordings()
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if p == scanCanary {
			return nil
		}
		parts := strings.Split(p, "/")
		if len(parts) != 3 || path.Ext(p) != ".json" {
			t.Errorf("%s: want <origin>/<version>/<name>.json", p)
			return nil
		}
		r, err := testkit.LoadRecording(fsys, parts[0], parts[1], strings.TrimSuffix(parts[2], ".json"))
		if err != nil {
			t.Error(err)
			return nil
		}
		if r.Origin != parts[0] || r.Version != parts[1] {
			t.Errorf("%s: origin/version %s/%s differ from the path", p, r.Origin, r.Version)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestLatestVersionComparesNumerically(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"x/2.1.9/a.json":   {Data: []byte("{}")},
		"x/2.1.296/a.json": {Data: []byte("{}")},
		"x/2.1.30/a.json":  {Data: []byte("{}")},
	}
	v, err := testkit.LatestVersion(fsys, "x")
	if err != nil || v != "2.1.296" {
		t.Fatalf("LatestVersion = %q, %v", v, err)
	}
}

func TestLatestVersionPrefersANumberedReleaseOverACommitId(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		versions []string
		want     string
	}{
		{"commit id first", []string{"b3c3bad", "0.9.0", "0.10.0"}, "0.10.0"},
		{"commit id last", []string{"0.10.0", "0.9.0", "b3c3bad"}, "0.10.0"},
		{"names compare by text", []string{"zzz9999", "abc1234"}, "zzz9999"},
		{"only a commit id", []string{"b3c3bad"}, "b3c3bad"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fsys := fstest.MapFS{}
			for _, v := range tc.versions {
				fsys["codex/"+v+"/a.json"] = &fstest.MapFile{Data: []byte("{}")}
			}
			if v, err := testkit.LatestVersion(fsys, "codex"); err != nil || v != tc.want {
				t.Fatalf("LatestVersion = %q, %v, want %q", v, err, tc.want)
			}
		})
	}
}

type recorder struct{ msgs []string }

func (r *recorder) Errorf(format string, args ...any) {
	r.msgs = append(r.msgs, fmt.Sprintf(format, args...))
}

func TestNotImplementedAnswers501(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	w := httptest.NewRecorder()
	testkit.NotImplemented(rec, w, "GET /x")
	if w.Code != 501 || !strings.Contains(w.Body.String(), "not implemented in double: GET /x") || len(rec.msgs) != 1 {
		t.Fatalf("code=%d body=%q msgs=%v", w.Code, w.Body.String(), rec.msgs)
	}
}
