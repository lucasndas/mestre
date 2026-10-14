package classes

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// PermissionsVersion is the version of the permissions.yaml format.
const PermissionsVersion = 1

// Permissions is a permissions.yaml (docs/classes/formato-das-permissoes.md).
// The same format is the ceiling of the workspace, read as a maximum, and the
// restrictions of a sheet. A nil field or list is absent: in a class it takes
// the default of the format; in a ceiling or a sheet it limits nothing.
type Permissions struct {
	Version  int           `yaml:"version"`
	Files    *FilesSpec    `yaml:"files"`
	Commands *CommandsSpec `yaml:"commands"`
	Git      *GitSpec      `yaml:"git"`
	Network  *NetworkSpec  `yaml:"network"`
	CLI      *CLISpec      `yaml:"cli"`
	Tools    *ToolsSpec    `yaml:"tools"`
	Operator *bool         `yaml:"operator"`
}

// FilesSpec is the files block.
type FilesSpec struct {
	Read  *[]string `yaml:"read"`
	Write *[]string `yaml:"write"`
	Deny  *[]string `yaml:"deny"`
}

// CommandsSpec is the commands block. Default is what a command that
// matches no pattern gets; absent in a class, ask.
type CommandsSpec struct {
	Allow   *[]string `yaml:"allow"`
	Ask     *[]string `yaml:"ask"`
	Deny    *[]string `yaml:"deny"`
	Default *Verdict  `yaml:"default"`
}

// GitSpec is the git block.
type GitSpec struct {
	Commit      *bool      `yaml:"commit"`
	Branches    *[]string  `yaml:"branches"`
	RequestPush *bool      `yaml:"request_push"`
	Workflows   *Workflows `yaml:"workflows"`
}

// NetworkSpec is the network block.
type NetworkSpec struct {
	Internet    *bool     `yaml:"internet"`
	Domains     *[]string `yaml:"domains"`
	Credentials *[]string `yaml:"credentials"`
}

// CLISpec is the cli block.
type CLISpec struct {
	Allow *[]string `yaml:"allow"`
}

// ToolsSpec is the tools block. Default is what a tool of kind mcp,
// subagent or other that matches no pattern gets; absent in a class, ask. A
// default at the top of the file is an unknown field
// (docs/classes/formato-das-permissoes.md#o-arquivo).
type ToolsSpec struct {
	Allow   *[]string `yaml:"allow"`
	Ask     *[]string `yaml:"ask"`
	Deny    *[]string `yaml:"deny"`
	Default *Verdict  `yaml:"default"`
}

// ErrInvalid wraps every error of an invalid permissions.yaml: the class is
// not saved (docs/classes/permissoes.md#regras-e-erros).
var ErrInvalid = errors.New("invalid permissions.yaml")

// ParsePermissions reads and checks a permissions.yaml: version 1, no
// unknown field, one document, the values of the enumerations, well-formed
// patterns and only the known placeholders. The credentials, which need the
// vault, are checked by CheckCredentials.
func ParsePermissions(data []byte) (*Permissions, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var p Permissions
	if err := dec.Decode(&p); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%w: empty", ErrInvalid)
		}
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: more than one YAML document", ErrInvalid)
	}
	if err := p.validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return &p, nil
}

// Placeholders the format knows, resolved by Build.
var knownPlaceholders = []string{"mesa.branch"}

var (
	placeholderRe = regexp.MustCompile(`\{([^{}]*)\}`)
	domainRe      = regexp.MustCompile(`^(\*\.)?([a-z0-9]([a-z0-9-]*[a-z0-9])?\.)*[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
	wordsRe       = regexp.MustCompile(`^[a-z0-9*][a-z0-9 *_-]*$`)
)

func (p *Permissions) validate() error {
	if p.Version != PermissionsVersion {
		return fmt.Errorf("version %d: this binary reads version %d", p.Version, PermissionsVersion)
	}
	if f := p.Files; f != nil {
		for _, l := range []struct {
			name     string
			list     *[]string
			absolute bool
		}{{"files.read", f.Read, true}, {"files.write", f.Write, false}, {"files.deny", f.Deny, true}} {
			for _, g := range deref(l.list) {
				if err := checkFileGlob(g, l.absolute); err != nil {
					return fmt.Errorf("%s: %q: %v", l.name, g, err)
				}
			}
		}
	}
	if c := p.Commands; c != nil {
		if err := checkTexts("commands", c.Allow, c.Ask, c.Deny); err != nil {
			return err
		}
		if c.Default != nil && !c.Default.valid() {
			return fmt.Errorf("commands.default: %q is not allow, ask or deny", *c.Default)
		}
	}
	if t := p.Tools; t != nil {
		if err := checkTexts("tools", t.Allow, t.Ask, t.Deny); err != nil {
			return err
		}
		if t.Default != nil && !t.Default.valid() {
			return fmt.Errorf("tools.default: %q is not allow, ask or deny", *t.Default)
		}
	}
	if g := p.Git; g != nil {
		for _, b := range deref(g.Branches) {
			if err := checkBranch(b); err != nil {
				return fmt.Errorf("git.branches: %q: %v", b, err)
			}
		}
		if g.Workflows != nil {
			switch *g.Workflows {
			case WorkflowsNever, WorkflowsAsk, WorkflowsAllow:
			default:
				return fmt.Errorf("git.workflows: %q is not never, ask or allow", *g.Workflows)
			}
		}
	}
	if n := p.Network; n != nil {
		for _, d := range deref(n.Domains) {
			if !domainRe.MatchString(d) {
				return fmt.Errorf("network.domains: %q is not a domain name (lowercase, optional *. prefix)", d)
			}
		}
		for _, c := range deref(n.Credentials) {
			if strings.TrimSpace(c) == "" {
				return fmt.Errorf("network.credentials: empty name")
			}
		}
	}
	if c := p.CLI; c != nil {
		for _, a := range deref(c.Allow) {
			if !wordsRe.MatchString(a) {
				return fmt.Errorf("cli.allow: %q is not a command of the CLI", a)
			}
		}
	}
	return nil
}

func checkFileGlob(g string, absolute bool) error {
	if strings.TrimSpace(g) == "" || strings.ContainsAny(g, "\x00\n") {
		return errors.New("empty or with a control character")
	}
	if strings.ContainsAny(g, "{}") {
		return errors.New("placeholders are not allowed in file globs")
	}
	slash := filepath.ToSlash(strings.ReplaceAll(g, `\`, "/"))
	if isAbsSlash(slash) && !absolute {
		return errors.New("absolute paths are only allowed in read and deny")
	}
	if !validGlob(slash) {
		return errors.New(`malformed glob or ".." segment`)
	}
	return nil
}

func checkTexts(block string, lists ...*[]string) error {
	names := []string{"allow", "ask", "deny"}
	for i, l := range lists {
		for _, s := range deref(l) {
			if strings.TrimSpace(s) == "" || strings.ContainsAny(s, "\x00\n\r") {
				return fmt.Errorf("%s.%s: %q: empty or with a line break", block, names[i], s)
			}
		}
	}
	return nil
}

func checkBranch(b string) error {
	if strings.TrimSpace(b) == "" || strings.ContainsAny(b, " \x00\n~^:?\\") {
		return errors.New("not a branch name pattern")
	}
	for _, m := range placeholderRe.FindAllStringSubmatch(b, -1) {
		if !slices.Contains(knownPlaceholders, m[1]) {
			return fmt.Errorf("unknown placeholder {%s} (known: %s)", m[1], strings.Join(knownPlaceholders, ", "))
		}
	}
	if !validGlob(placeholderRe.ReplaceAllString(b, "x")) {
		return errors.New("malformed glob")
	}
	return nil
}

// CredentialLookup is the vault as the check of network.credentials sees it:
// the domains of a site credential and the classes it lists.
type CredentialLookup interface {
	SiteCredential(name string) (domains, classes []string, ok bool)
}

// CheckCredentials checks network.credentials of the class: each name exists
// in the vault, lists the class, and its domains are reachable by
// network.domains. Any failure means the class is not saved.
func (p *Permissions) CheckCredentials(class string, vault CredentialLookup) error {
	if p.Network == nil || len(deref(p.Network.Credentials)) == 0 {
		return nil
	}
	if vault == nil {
		return fmt.Errorf("%w: network.credentials: the vault is not available to check them", ErrInvalid)
	}
	internet := p.Network.Internet == nil || *p.Network.Internet
	allowed := deref(p.Network.Domains)
	for _, name := range deref(p.Network.Credentials) {
		domains, classes, ok := vault.SiteCredential(name)
		if !ok {
			return fmt.Errorf("%w: network.credentials: %q is not a site credential of the vault", ErrInvalid, name)
		}
		if !slices.Contains(classes, class) {
			return fmt.Errorf("%w: network.credentials: %q does not list the class %q", ErrInvalid, name, class)
		}
		for _, d := range domains {
			if !domainAllowed(internet, allowed, d) {
				return fmt.Errorf("%w: network.credentials: the domain %q of %q is not in network.domains", ErrInvalid, d, name)
			}
		}
	}
	return nil
}

func domainAllowed(internet bool, domains []string, host string) bool {
	if !internet {
		return false
	}
	if len(domains) == 0 {
		return true
	}
	for _, d := range domains {
		if matchDomain(d, host) {
			return true
		}
	}
	return false
}

func deref(l *[]string) []string {
	if l == nil {
		return nil
	}
	return *l
}

// isAbsSlash reports whether a slash path is absolute on either system:
// /x, //host/share or C:/x.
func isAbsSlash(p string) bool {
	if strings.HasPrefix(p, "/") {
		return true
	}
	return len(p) >= 3 && p[1] == ':' && p[2] == '/' && (p[0]|0x20) >= 'a' && (p[0]|0x20) <= 'z'
}
