package protocol_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/logging"
	"github.com/mestre-ia/mestre/internal/protocol"
	"github.com/mestre-ia/mestre/pkg/clock"
)

func TestTheEnvelopeEncodesAsDocumented(t *testing.T) {
	t.Parallel()
	env := protocol.Envelope{
		ID: "01J9Z0000000000000000000A1", Type: "terminal.open", MachineID: "m1", Epoch: 7,
		TS:      protocol.Timestamp{Time: time.Date(2026, 10, 13, 15, 4, 5, 123456789, time.FixedZone("x", -3*3600))},
		Payload: json.RawMessage(`{"a":1}`), ReplyTo: "01J9Z0000000000000000000A2", Ack: true,
	}
	b, err := protocol.Encode(env)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["ts"] != "2026-10-13T18:04:05.123Z" {
		t.Fatalf("ts = %v, want UTC with milliseconds", m["ts"])
	}
	for _, k := range []string{"id", "type", "machine_id", "epoch", "ts", "payload", "reply_to", "ack"} {
		if _, ok := m[k]; !ok {
			t.Errorf("field %q missing in %s", k, b)
		}
	}
	back, perr := protocol.Decode(b)
	if perr != nil || back.ID != env.ID || back.Epoch != 7 || back.MachineID != "m1" || !back.Ack || back.ReplyTo != env.ReplyTo {
		t.Fatalf("round trip = %+v, %v", back, perr)
	}
}

func TestAnEnvelopeOfTheBrowserHasNoMachineAndNoEpoch(t *testing.T) {
	t.Parallel()
	b, err := protocol.Encode(protocol.Envelope{ID: "01J9Z0000000000000000000A1", Type: "ui.subscribe", TS: protocol.Timestamp{Time: time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)}})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte("machine_id")) || bytes.Contains(b, []byte("epoch")) || bytes.Contains(b, []byte("ack")) {
		t.Fatalf("browser envelope = %s", b)
	}
	e, perr := protocol.Decode(b)
	if perr != nil {
		t.Fatal(perr)
	}
	if perr := e.CheckSender(protocol.Peer{Role: protocol.RoleBrowser, ID: "s1"}); perr != nil {
		t.Fatalf("CheckSender(browser) = %v", perr)
	}
	if perr := e.CheckSender(protocol.Peer{Role: protocol.RoleMachine, ID: "m1"}); perr == nil || perr.Code != protocol.CodeInvalidPayload {
		t.Fatalf("CheckSender(machine) = %v, want invalid_payload: between machines machine_id and epoch are required", perr)
	}
}

func TestUnknownFieldsOfTheEnvelopeAndThePayloadAreIgnored(t *testing.T) {
	t.Parallel()
	raw := `{"id":"01J9Z0000000000000000000A1","type":"terminal.status","machine_id":"m","epoch":1,"ts":"2026-10-13T00:00:00.000Z",
		"future":"field","payload":{"terminal_id":"t","state":"idle","new_field":{"x":1}}}`
	env, perr := protocol.Decode([]byte(raw))
	if perr != nil {
		t.Fatal(perr)
	}
	p, perr := protocol.DefaultRegistry().Decode(env)
	if perr != nil || p.(*protocol.TerminalStatus).State != "idle" {
		t.Fatalf("Decode = %+v, %v", p, perr)
	}
}

func TestMalformedEnvelopesAreRefusedWithInvalidPayload(t *testing.T) {
	t.Parallel()
	const id = `"id":"01J9Z0000000000000000000A1"`
	const ts = `"ts":"2026-10-13T00:00:00.000Z"`
	for name, raw := range map[string]string{
		"not json":              `nope`,
		"not an object":         `[1]`,
		"no id":                 `{"type":"x.y",` + ts + `,"payload":{}}`,
		"an id that is no ULID": `{"id":"abc","type":"x.y",` + ts + `,"payload":{}}`,
		"a type with spaces":    `{` + id + `,"type":"x y",` + ts + `,"payload":{}}`,
		"a type in capitals":    `{` + id + `,"type":"X.Y",` + ts + `,"payload":{}}`,
		"no ts":                 `{` + id + `,"type":"x.y","payload":{}}`,
		"a ts that is no date":  `{` + id + `,"type":"x.y","ts":"yesterday","payload":{}}`,
		"no payload":            `{` + id + `,"type":"x.y",` + ts + `}`,
		"a payload array":       `{` + id + `,"type":"x.y",` + ts + `,"payload":[]}`,
		"a payload null":        `{` + id + `,"type":"x.y",` + ts + `,"payload":null}`,
		"a bad reply_to":        `{` + id + `,"type":"x.y",` + ts + `,"payload":{},"reply_to":"zzz"}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, perr := protocol.Decode([]byte(raw)); perr == nil || perr.Code != protocol.CodeInvalidPayload {
				t.Fatalf("Decode = %v, want invalid_payload", perr)
			}
		})
	}
}

func TestOneMiBIsTheLimitOfAMessage(t *testing.T) {
	t.Parallel()
	pad := func(n int) protocol.Envelope {
		return protocol.Envelope{ID: "01J9Z0000000000000000000A1", Type: "x.y", TS: protocol.Timestamp{Time: time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)},
			Payload: json.RawMessage(`{"d":"` + strings.Repeat("a", n) + `"}`)}
	}
	if _, err := protocol.Encode(pad(protocol.MaxMessage + 1)); err == nil || err.(*protocol.Error).Code != protocol.CodeTooLarge {
		t.Fatalf("Encode over the limit = %v", err)
	}
	// just under the limit works; what matters is the whole JSON
	small := pad(0)
	b, _ := protocol.Encode(small)
	overhead := len(b)
	ok, err := protocol.Encode(pad(protocol.MaxMessage - overhead))
	if err != nil || len(ok) != protocol.MaxMessage {
		t.Fatalf("Encode at the limit = %d bytes, %v", len(ok), err)
	}
	if _, perr := protocol.Decode(ok); perr != nil {
		t.Fatal(perr)
	}
	if _, perr := protocol.Decode(append(ok, ' ')); perr == nil || perr.Code != protocol.CodeTooLarge {
		t.Fatalf("Decode over the limit = %v", perr)
	}
}

func TestULIDsRoundTripAndKeepTheirTime(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC))
	gen := protocol.NewIDGen(clk)
	a := gen.New()
	clk.Advance(time.Millisecond)
	b := gen.New()
	if a == b || a.String() >= b.String() {
		t.Fatalf("%s then %s: ids must be unique and sort by time", a, b)
	}
	for _, u := range []protocol.ULID{a, b, {}, {0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}} {
		back, err := protocol.ParseULID(u.String())
		if err != nil || back != u || len(u.String()) != 26 {
			t.Fatalf("%v -> %q -> %v, %v", u, u.String(), back, err)
		}
	}
	if _, err := protocol.ParseULID(strings.ToLower(a.String())); err != nil {
		t.Fatalf("lowercase: %v", err)
	}
	for _, bad := range []string{"", "short", strings.Repeat("A", 27), "01J9Z000000000000000000ILO", "8ZZZZZZZZZZZZZZZZZZZZZZZZZ"} {
		if _, err := protocol.ParseULID(bad); err == nil {
			t.Errorf("ParseULID(%q) accepted", bad)
		}
	}
}

// ---- the catalogue ----

var docRow = regexp.MustCompile("^\\| `([^`|]+)`(?: / `([^`|]+)`)? \\|")

// The registry has every type of the table of mensagens.md, and the table
// has every type of the registry that the doc names (the ones the doc leaves
// to the Guardian are not here).
func TestTheRegistryHasEveryTypeOfTheCatalogueOfTheDocument(t *testing.T) {
	t.Parallel()
	doc, err := os.ReadFile("../../docs/conectividade/mensagens.md")
	if err != nil {
		t.Fatal(err)
	}
	section, _, _ := strings.Cut(string(doc), "## Guardião da Torre")
	reg := protocol.DefaultRegistry()
	var inDoc []string
	for _, line := range strings.Split(section, "\n") {
		m := docRow.FindStringSubmatch(line)
		if m == nil || m[1] == "type" {
			continue
		}
		for _, typ := range []string{m[1], m[2]} {
			if typ == "" {
				continue
			}
			if prefix, ok := strings.CutSuffix(typ, ".*"); ok {
				found := false
				for _, r := range reg.Types() {
					found = found || strings.HasPrefix(r, prefix+".")
				}
				if !found {
					t.Errorf("the document names %s and the registry has nothing like it", typ)
				}
				continue
			}
			inDoc = append(inDoc, typ)
			if _, ok := reg.Spec(typ); !ok {
				t.Errorf("the document names %s and the registry does not have it", typ)
			}
		}
	}
	if len(inDoc) < 30 {
		t.Fatalf("read only %d types from the document: the table format changed?", len(inDoc))
	}
	// the registry has nothing the document does not mention (a type must be
	// documented in the same change that adds it)
	text := string(doc) + readFile(t, "../../docs/conectividade/protocolo.md")
	for _, typ := range reg.Types() {
		if !strings.Contains(text, "`"+typ+"`") && !strings.Contains(text, "`"+typ+" ") && !strings.Contains(text, "`"+strings.SplitN(typ, ".", 2)[0]+".*`") {
			t.Errorf("type %s is not in mensagens.md or protocolo.md", typ)
		}
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Every type refuses an empty payload if it has a required field, and
// accepts it if not (or fails its own check, which must say so).
func TestEveryTypeRequiresItsRequiredFields(t *testing.T) {
	t.Parallel()
	reg := protocol.DefaultRegistry()
	for _, typ := range reg.Types() {
		t.Run(typ, func(t *testing.T) {
			t.Parallel()
			spec, _ := reg.Spec(typ)
			_, perr := reg.Decode(protocol.Envelope{Type: typ, Payload: json.RawMessage(`{}`)})
			needs := requires(reflect.TypeOf(spec.Payload))
			if needs && (perr == nil || perr.Code != protocol.CodeInvalidPayload) {
				t.Fatalf("an empty payload of %s was accepted", typ)
			}
			if !needs && perr != nil && !strings.Contains(perr.Message, "required") && !validates(spec.Payload) {
				t.Fatalf("an empty payload of %s was refused: %v", typ, perr)
			}
		})
	}
}

func requires(t reflect.Type) bool {
	if t.Kind() != reflect.Struct {
		return false
	}
	for i := range t.NumField() {
		if t.Field(i).Tag.Get("proto") == "req" {
			return true
		}
	}
	return false
}

func validates(p any) bool {
	_, ok := reflect.New(reflect.TypeOf(p)).Interface().(interface{ Validate() error })
	if ok {
		return true
	}
	_, ok = p.(interface{ Validate() error })
	return ok
}

func TestPayloadsAreCheckedBeforeTheyAreSent(t *testing.T) {
	t.Parallel()
	reg := protocol.DefaultRegistry()
	for name, tc := range map[string]struct {
		typ     string
		payload any
		ok      bool
	}{
		"a good terminal.input":        {protocol.TypeTerminalInput, protocol.TerminalInput{TerminalID: "t", Mode: "raw", Data: "x"}, true},
		"a mode that does not exist":   {protocol.TypeTerminalInput, protocol.TerminalInput{TerminalID: "t", Mode: "loud", Data: "x"}, false},
		"a missing terminal":           {protocol.TypeTerminalInput, protocol.TerminalInput{Mode: "raw", Data: "x"}, true}, // empty strings are present
		"a good inbox.result":          {protocol.TypeInboxResult, protocol.InboxResult{ItemID: "i", Status: "busy"}, true},
		"an inbox.result of nonsense":  {protocol.TypeInboxResult, protocol.InboxResult{ItemID: "i", Status: "maybe"}, false},
		"needs_captcha needs an image": {protocol.TypeAgentSiteLoginStatus, protocol.AgentSiteLoginStatus{AttemptID: "a", State: "needs_captcha"}, false},
		"needs_captcha with the image": {protocol.TypeAgentSiteLoginStatus, protocol.AgentSiteLoginStatus{AttemptID: "a", State: "needs_captcha", XferID: "x"}, true},
		"failed needs a reason":        {protocol.TypeAgentSiteLoginStatus, protocol.AgentSiteLoginStatus{AttemptID: "a", State: "failed"}, false},
		"a cli.call without a caller":  {protocol.TypeCLICall, map[string]any{"cmd": "done"}, false},
		"a cli.call with its caller":   {protocol.TypeCLICall, map[string]any{"cmd": "done", "caller": map[string]string{"agent_id": "a", "terminal_id": "t", "machine_id": "m"}}, true},
		"a transfer with a bad kind":   {protocol.TypeXferBegin, protocol.XferBegin{XferID: "01J9Z0000000000000000000A1", Kind: "tarball", SHA256: strings.Repeat("0", 64)}, false},
		"a transfer with a bad sha":    {protocol.TypeXferBegin, protocol.XferBegin{XferID: "01J9Z0000000000000000000A1", Kind: "file", SHA256: "abc"}, false},
		"a good transfer":              {protocol.TypeXferBegin, protocol.XferBegin{XferID: "01J9Z0000000000000000000A1", Kind: "file", SHA256: strings.Repeat("0", 64)}, true},
		"a hello without a range":      {protocol.TypeHello, protocol.Hello{Version: "1", Sheet: protocol.Object{}, Certificate: &protocol.Certificate{GuildID: "g", Epoch: 1, CitadelMachineID: "c", Signature: "s"}}, false},
		"a cert.renew for a role":      {protocol.TypeCertRenew, protocol.CertRenew{Role: "cozinha", CSR: "x"}, false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := reg.Marshal(tc.typ, tc.payload, false)
			if (err == nil) != tc.ok {
				t.Fatalf("Marshal(%s) = %v, want ok = %v", tc.typ, err, tc.ok)
			}
		})
	}
}

func TestTheAnswerToGuildEpochIsACertificate(t *testing.T) {
	t.Parallel()
	reg := protocol.DefaultRegistry()
	req := protocol.Envelope{Type: protocol.TypeGuildEpoch, Payload: json.RawMessage(`{}`)}
	if _, perr := reg.Decode(req); perr != nil {
		t.Fatalf("the request has no fields: %v", perr)
	}
	reply := protocol.Envelope{Type: protocol.TypeGuildEpoch, ReplyTo: "01J9Z0000000000000000000A1", Payload: json.RawMessage(`{}`)}
	if _, perr := reg.Decode(reply); perr == nil {
		t.Fatal("an answer without the certificate was accepted")
	}
}

func TestASecretPrintsRedactedAndIsKeptOutOfTheLogs(t *testing.T) {
	t.Parallel()
	s := protocol.Secret("hunter2")
	if fmt.Sprint(s) != logging.Redacted || fmt.Sprintf("%v %+v %#v", s, s, s) != logging.Redacted+" "+logging.Redacted+` "`+logging.Redacted+`"` {
		t.Fatalf("Secret printed as %v", s)
	}
	login := protocol.AgentSiteLogin{AttemptID: "a", Username: "u", Secret: "hunter2"}
	if got := fmt.Sprintf("%+v", login); strings.Contains(got, "hunter2") {
		t.Fatalf("the struct printed the secret: %s", got)
	}
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	env := protocol.Envelope{ID: "01J9Z0000000000000000000A1", Type: protocol.TypeAgentSiteLogin, Payload: json.RawMessage(`{"secret":"hunter2"}`)}
	log.LogAttrs(context.Background(), slog.LevelInfo, "got", slog.Any("env", env))
	if strings.Contains(buf.String(), "hunter2") || !strings.Contains(buf.String(), env.Type) {
		t.Fatalf("log = %s", buf.String())
	}
	if b, _ := json.Marshal(login); !strings.Contains(string(b), "hunter2") {
		t.Fatal("on the wire the value must be there")
	}
}

func TestAnErrorIsRetryableAsTheTableSays(t *testing.T) {
	t.Parallel()
	retryable := []string{protocol.CodeHashMismatch, protocol.CodeBusyTerminal, protocol.CodeUnavailable, protocol.CodeInternal}
	for _, code := range []string{
		protocol.CodeUnknownType, protocol.CodeInvalidPayload, protocol.CodeTooLarge, protocol.CodeNotFound, protocol.CodeStaleEpoch,
		protocol.CodeMissingPrerequisite, protocol.CodeHashMismatch, protocol.CodeBusyTerminal, protocol.CodeUnavailable,
		protocol.CodeAffectsGuardian, protocol.CodeForbidden, protocol.CodeInternal,
	} {
		if !protocol.KnownCode(code) {
			t.Errorf("%s is not known", code)
		}
		if got := protocol.NewError(code, "x").Retryable; got != slices.Contains(retryable, code) {
			t.Errorf("%s retryable = %v", code, got)
		}
	}
}

// The codes of the table of mensagens.md are exactly the ones of the package.
func TestTheErrorCodesAreTheOnesOfTheDocument(t *testing.T) {
	t.Parallel()
	doc := readFile(t, "../../docs/conectividade/mensagens.md")
	_, table, _ := strings.Cut(doc, "## Códigos de erro")
	re := regexp.MustCompile("(?m)^\\| `([a-z_]+)` \\| (sim|não) \\|")
	found := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(table, -1) {
		found[m[1]] = true
		if !protocol.KnownCode(m[1]) {
			t.Errorf("the document has the code %s and the package does not", m[1])
		}
		if want := m[2] == "sim"; protocol.NewError(m[1], "x").Retryable != want {
			t.Errorf("%s: retryable in the document is %v", m[1], want)
		}
	}
	if len(found) != 12 {
		t.Fatalf("read %d codes from the document", len(found))
	}
}
