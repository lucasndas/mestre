package classes

import (
	"net/url"
	"slices"
	"strings"

	"github.com/lucasndas/mestre/internal/providers"
)

// The groups of medio (docs/orquestracao/cadeia-de-aprovacao.md#condições-da-mesa):
// each origin of a medio part, the row or the signal that gave it, belongs
// to one, and the Mestre da mesa decides a medio only if every group of every
// part is one the owner lets it decide. GroupNone is an origin with no group
// (what the engine cannot analyse, the reading of the shell, the send of the
// table branch, the owner's adjustment): it is always the Grão-Mestre's.
const (
	GroupNone            = "sem_grupo"
	GroupNetwork         = "rede"
	GroupDelete          = "apagar"
	GroupContainers      = "conteineres_e_vms"
	GroupPackages        = "pacotes_e_executores"
	GroupInterpreter     = "interpretador_com_codigo"
	GroupChangedScript   = "script_alterado"
	GroupNotListed       = "fora_da_lista"
	GroupUnseenWrite     = "escrita_sem_caminho"
	GroupInstallSystem   = "instalacao_e_sistema"
	GroupOutsideWorktree = "fora_do_worktree"
)

// DefaultMesaGroups are the groups the Mestre da mesa decides by default,
// in the default content of cadeia.mesa_decide.
var DefaultMesaGroups = []string{GroupNetwork, GroupDelete, GroupContainers, GroupPackages, GroupInterpreter, GroupChangedScript, GroupNotListed}

// MesaGroups are all the groups cadeia.mesa_decide can name.
var MesaGroups = []string{GroupNetwork, GroupDelete, GroupContainers, GroupPackages, GroupInterpreter, GroupChangedScript, GroupNotListed,
	GroupUnseenWrite, GroupInstallSystem, GroupOutsideWorktree}

// signalGroups map the group name that the rule of a signal carries to the
// group of the chain.
var signalGroups = map[string]string{
	"rede":                     GroupNetwork,
	"apagar":                   GroupDelete,
	"contêiner ou VM":          GroupContainers,
	"interpretador com código": GroupInterpreter,
	groupChangedScript:         GroupChangedScript,
	groupUnseenWrite:           GroupUnseenWrite,
	"instalação ou configuração do sistema": GroupInstallSystem,
}

// packageRunners are the rows of "fora do worktree e configuração" that run
// a package from the net or install a package of the project: group
// pacotes_e_executores; the others of that row are instalacao_e_sistema.
var packageRunners = []string{"npx *", "pnpm dlx *", "bunx *", "npm exec*", "npm x *", "pnpm exec*", "yarn exec*", "yarn dlx *", "bun x *",
	"pip install*", "pip3 install*"}

// originGroup is the group of the chain of an origin, from the rule that
// the classification wrote for it.
func originGroup(rule string) string {
	switch {
	case strings.HasPrefix(rule, "sinal de "):
		name, _, _ := strings.Cut(strings.TrimPrefix(rule, "sinal de "), ":")
		if g, ok := signalGroups[name]; ok {
			return g
		}
		return GroupNone
	case strings.HasPrefix(rule, "caminho fora do worktree"):
		return GroupOutsideWorktree
	case strings.HasPrefix(rule, groupNotListed+":"):
		return GroupNotListed
	case strings.HasPrefix(rule, "web: domínio fora"):
		return GroupNetwork
	case strings.HasPrefix(rule, "rede:"):
		return GroupNetwork
	case strings.HasPrefix(rule, "apagar em massa:"):
		return GroupDelete
	case strings.HasPrefix(rule, "contêineres e serviços:"):
		pat := strings.TrimSpace(strings.TrimPrefix(rule, "contêineres e serviços:"))
		for _, p := range []string{"docker ", "podman ", "docker-compose "} {
			if strings.HasPrefix(pat, p) {
				return GroupContainers
			}
		}
		return GroupInstallSystem
	case strings.HasPrefix(rule, "fora do worktree e configuração:"):
		pat := strings.TrimSpace(strings.TrimPrefix(rule, "fora do worktree e configuração:"))
		if slices.Contains(packageRunners, pat) {
			return GroupPackages
		}
		return GroupInstallSystem
	}
	return GroupNone
}

// groupsOf are the groups of the origins of a medio, sorted and without
// repetition; nil for another risk.
func groupsOf(o riskOut) []string {
	if o.risk != RiskMedium {
		return nil
	}
	var out []string
	for _, r := range o.origins() {
		if g := originGroup(r); !slices.Contains(out, g) {
			out = append(out, g)
		}
	}
	slices.Sort(out)
	return out
}

// ActionHosts are the hosts a tool call names: the URL of a web tool, and the
// URLs and hosts (user@host:path, an IPv4 address, localhost) among the
// arguments of a shell command. unreadable is true when a URL has no host the
// engine can read. The chain checks each against the domains of the class.
func ActionHosts(t *providers.Tool) (hosts []string, unreadable bool) {
	if t == nil {
		return nil, false
	}
	add := func(raw string) {
		if u, err := url.Parse(raw); err == nil && u.Hostname() != "" {
			hosts = append(hosts, strings.ToLower(u.Hostname()))
			return
		}
		unreadable = true
	}
	if t.URL != "" {
		add(t.URL)
	}
	for _, f := range strings.Fields(t.Command) {
		f = strings.Trim(f, `"'`+"`;&|()")
		switch {
		case strings.Contains(f, "://"):
			if i := strings.Index(f, "://"); i > 0 {
				// the scheme may be glued to a flag or a name: --url=https://x
				j := i
				for j > 0 && (f[j-1] >= 'a' && f[j-1] <= 'z' || f[j-1] >= 'A' && f[j-1] <= 'Z' || f[j-1] >= '0' && f[j-1] <= '9' || f[j-1] == '+' || f[j-1] == '.' || f[j-1] == '-') {
					j--
				}
				add(f[j:])
			}
		default:
			if h, ok := hostOf(f); ok {
				hosts = append(hosts, h)
			}
		}
	}
	return hosts, unreadable
}

// DomainListed says whether host is in the list of domains, with the same
// matching as the network.domains of the rules.
func DomainListed(list []string, host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, d := range list {
		if matchDomain(d, host) {
			return true
		}
	}
	return false
}
