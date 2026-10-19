package github_test

import (
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/github"
	"github.com/lucasndas/mestre/internal/gitx"
)

// Part of CA-CLI-GH-07: the formats the scan knows, the exceptions it has, and
// the value it never shows.
func TestTheScanFindsTheSecretsOfKnownFormatsAndNeverShowsThem(t *testing.T) {
	t.Parallel()
	kinds := map[string]string{
		`token = "ghp_` + strings.Repeat("a1B2", 9) + `"`:                          "github_token",
		`bot 123456789:` + strings.Repeat("AbC_d", 7):                              "telegram_bot_token",
		`jwt = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.abc123def456ghi"`: "jwt",
		`key = AKIA` + strings.Repeat("A1", 8):                                     "aws_access_key",
		`curl -H "Authorization: Bearer ` + strings.Repeat("z9", 20) + `"`:         "authorization_header",
		`const k = "sk-` + strings.Repeat("q", 24) + `"`:                           "sk_key",
		`-----BEGIN RSA PRIVATE KEY-----`:                                          "private_key",
		`password: "` + strings.Repeat("x", 30) + `"`:                              "secret_assignment",
		`xoxb-1234567890-abcdefghijk`:                                              "slack_token",
	}
	for text, want := range kinds {
		got := github.ScanSecrets([]gitx.AddedLine{{Path: "src/x.ts", Line: 12, Text: text}})
		if len(got) != 1 || got[0].Kind != want || got[0].Path != "src/x.ts" || got[0].Line != 12 {
			t.Errorf("%q: %+v, want %s", text, got, want)
			continue
		}
		if s := got[0].String(); !strings.HasPrefix(s, "src/x.ts:12 ") || strings.Contains(s, "a1B2") || strings.Contains(s, "zzz") {
			t.Errorf("the finding says too much: %q", s)
		}
	}
	for _, fine := range []string{
		`token = process.env.GH_TOKEN`, `const t = process.env["TOKEN"]`, `password = os.environ["PASSWORD"]`, `secret: "${SECRET_VALUE_FROM_CI_PIPELINE}"`,
		`token = "your_token_here_000000000"`, `ghp_exampleexampleexampleexample`, `Authorization: Bearer ${TOKEN}`,
		`password = "short"`, `// a token is a secret`, `nothing here`,
	} {
		if got := github.ScanSecrets([]gitx.AddedLine{{Path: "a", Line: 1, Text: fine}}); len(got) != 0 {
			t.Errorf("%q was taken for a secret: %+v", fine, got)
		}
	}
	// a literal on a line that also reads the environment is still found
	mixed := `const t = process.env.X || "ghp_` + strings.Repeat("a1B2", 9) + `"`
	if got := github.ScanSecrets([]gitx.AddedLine{{Path: "a", Line: 1, Text: mixed}}); len(got) != 1 || got[0].Kind != "github_token" {
		t.Errorf("a literal next to a read of the environment: %+v", got)
	}
}
