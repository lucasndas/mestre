package attach

import (
	"net/url"
	"strings"

	"github.com/mestre-ia/mestre/internal/i18n"
)

// usageLine is the syntax of the command.
const usageLine = "mestre attach <character> [--workspace <name>] [--write] [--server <address>] [--detach-key <key>]"

// options are the arguments of `mestre attach`.
type options struct {
	character, workspace, server string
	write, json                  bool
	detach                       byte
	detachName                   string
	serverURL                    *url.URL
	// instance and dir choose the folders of the instance, as in `mestre setup-code`.
	instance, dir string
}

// wantsJSON looks for --json before the arguments are parsed, so that even a
// refusal to parse them is answered in the form asked.
func wantsJSON(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if a == "--json" {
			return true
		}
	}
	return false
}

// parseArgs reads the arguments. Flags may come before or after the name; `--`
// ends them.
func parseArgs(args []string, defaultInstance string) (options, error) {
	o := options{instance: defaultInstance, detach: 0x1d, detachName: "Ctrl-]"}
	var rest []string
	bad := func(m i18n.Msg) error { return fail(codeUsage, i18n.M("person.hint_usage", "usage", usageLine), m) }
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			rest = append(rest, args[i+1:]...)
			break
		}
		if !isFlag(a) {
			rest = append(rest, a)
			continue
		}
		name, val, hasVal := strings.Cut(strings.TrimPrefix(a, "--"), "=")
		value := func() (string, error) {
			if hasVal {
				return val, nil
			}
			if i+1 >= len(args) {
				return "", bad(i18n.M("person.needs_value", "name", name))
			}
			i++
			return args[i], nil
		}
		var err error
		switch name {
		case "write":
			o.write = true
		case "json":
			o.json = true
		case "workspace":
			o.workspace, err = value()
		case "server":
			o.server, err = value()
		case "instance":
			o.instance, err = value()
		case "dir":
			o.dir, err = value()
		case "detach-key":
			var k string
			if k, err = value(); err == nil {
				if o.detach, err = parseKey(k); err == nil {
					o.detachName = k
				}
			}
		case "help":
			return o, bad(i18n.M("person.help"))
		default:
			err = bad(i18n.M("person.unknown_flag", "name", name))
		}
		if err != nil {
			return o, err
		}
	}
	if len(rest) != 1 || strings.TrimSpace(rest[0]) == "" {
		return o, bad(i18n.M("person.needs_a_character"))
	}
	o.character = strings.TrimSpace(rest[0])
	if o.server != "" {
		u, err := normalizeServer(o.server)
		if err != nil {
			return o, err
		}
		o.serverURL = u
	}
	return o, nil
}

// isFlag: only --[a-z][a-z-]* (with an optional =value) is a flag, as in the
// conventions of the CLI.
func isFlag(a string) bool {
	if !strings.HasPrefix(a, "--") || len(a) < 3 {
		return false
	}
	name, _, _ := strings.Cut(a[2:], "=")
	if name == "" || name[0] < 'a' || name[0] > 'z' {
		return false
	}
	for _, c := range name {
		if (c < 'a' || c > 'z') && c != '-' {
			return false
		}
	}
	return true
}

// parseKey reads the detach key: "ctrl-]", "ctrl-a", "^]"… A control key is
// the only kind of key that no program types by accident.
func parseKey(s string) (byte, error) {
	l := strings.ToLower(strings.TrimSpace(s))
	var c string
	switch {
	case strings.HasPrefix(l, "ctrl-"):
		c = l[len("ctrl-"):]
	case strings.HasPrefix(l, "ctrl+"):
		c = l[len("ctrl+"):]
	case strings.HasPrefix(l, "^"):
		c = l[1:]
	default:
		return 0, fail(codeUsage, i18n.M("person.bad_detach_key_hint"), i18n.M("person.bad_detach_key", "value", s))
	}
	if len(c) == 1 {
		var b byte
		switch ch := c[0]; {
		case ch >= 'a' && ch <= 'z':
			b = ch - 'a' + 1
		case ch == '[' || ch == '\\' || ch == ']' || ch == '^' || ch == '_':
			b = ch - '@'
		}
		// Tab, Enter and Esc are keys of the terminal itself: the escape sequences begin with Esc
		if b != 0 && b != '\t' && b != '\n' && b != '\r' && b != 0x1b {
			return b, nil
		}
	}
	return 0, fail(codeUsage, i18n.M("person.bad_detach_key_hint"), i18n.M("person.bad_detach_key", "value", s))
}
