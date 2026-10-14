package classes

import (
	"net/url"
	"regexp"
	"strings"
)

// The risk signals (docs/classes/classificacao-de-risco.md#sinais-de-risco):
// a part that matches no row of the table is baixo unless it shows one of
// them, which makes it medio. The path floors (outside the worktree, the
// secrets and the Mestre) and what the motor cannot analyse hold over every
// part, as before; two of the signals (a URL or host of a domain the
// effective rules do not list, and a global install flag of a package
// manager) also hold over the rows of the table.

// signalPrograms are the programs that are a signal by their name alone,
// with the group that names it in the rule.
var signalPrograms = map[string]string{}

func init() {
	for group, names := range map[string]string{
		"rede": "ping ping6 dig nslookup host drill traceroute traceroute6 tracert tracepath mtr ftp tftp lftp socat nmap whois " +
			"aria2c axel xh https grpcurl websocat mail mailx sendmail mutt rclone s3cmd ngrok cloudflared tailscale " +
			"invoke-restmethod irm test-netconnection tnc resolve-dnsname start-bitstransfer bitsadmin certutil",
		"apagar": "unlink shred srm wipe trash trash-put del erase remove-item ri rd rmdir clear-content clc remove-itemproperty rp",
		"instalação ou configuração do sistema": "apt apt-get aptitude dpkg yum dnf rpm zypper pacman apk emerge snap flatpak port " +
			"nix-env pkg pipx pip pip3 conda mamba micromamba gem sdk asdf mise rustup nvm volta fnm pyenv rbenv " +
			"kill pkill killall taskkill stop-process spps shutdown reboot poweroff halt restart-computer stop-computer " +
			"crontab at schtasks systemd-run reg regedit setx netsh wmic bcdedit diskpart format mount umount fdisk parted " +
			"losetup sysctl modprobe insmod rmmod iptables ip6tables nft ufw firewall-cmd useradd userdel usermod groupadd " +
			"passwd chown chgrp chattr defaults scutil set-itemproperty sp new-itemproperty set-executionpolicy " +
			"install-module install-package install-script update-module msiexec rundll32 regsvr32 dism sfc " +
			"new-service remove-service restart-service set-service update-alternatives ldconfig",
		"contêiner ou VM": "nerdctl buildah skopeo lima limactl colima vagrant virsh virt-install vboxmanage multipass " +
			"lxc lxd incus kind minikube k3d k3s orb orbctl finch new-vm start-vm stop-vm",
		"leitura do shell": "alias unalias function shopt enable hash trap set-alias new-alias sal nal",
	} {
		for _, n := range strings.Fields(names) {
			signalPrograms[n] = group
		}
	}
}

// signalPrefixes are program name prefixes that are a signal.
var signalPrefixes = map[string]string{"qemu": "contêiner ou VM", "mkfs": "instalação ou configuração do sistema"}

// signalPatterns are the subcommands that are a signal, matched like the
// rows of the table; the reach of a pattern sees them too.
var signalPatterns = []riskRow{
	{risk: RiskMedium, group: "rede", pattern: "git clone*"},
	{risk: RiskMedium, group: "rede", pattern: "git ls-remote*"},
	{risk: RiskMedium, group: "rede", pattern: "git submodule*"},
	{risk: RiskMedium, group: "rede", pattern: "git send-email*"},
	{risk: RiskMedium, group: "rede", pattern: "git archive --remote*"},
	{risk: RiskMedium, group: "apagar", pattern: "git rm*"},
	{risk: RiskMedium, group: "interpretador com código", pattern: "deno eval*"},
	{risk: RiskMedium, group: "interpretador com código", pattern: "deno repl*"},
	{risk: RiskMedium, group: groupUnseenWrite, pattern: "git apply*"},
	{risk: RiskMedium, group: groupUnseenWrite, pattern: "git am*"},
}

// groupUnseenWrite is the signal of the programs that write files the
// command does not name: the archive an extraction unpacks, the patch an
// application applies. The fixed rules and the files block only see the
// paths of the line, so these are never baixo.
const groupUnseenWrite = "escrita sem caminho visível"

// unseenWriters are the programs of the signal: each writes what it does
// not name when unseenWrite says so (an extraction, not a listing).
var unseenWriters = map[string]bool{
	"tar": true, "bsdtar": true, "gtar": true, "unzip": true, "7z": true, "7za": true, "7zr": true, "7zz": true,
	"unrar": true, "rar": true, "cpio": true, "pax": true, "ar": true, "jar": true, "expand-archive": true, "patch": true,
}

// unseenWrite reports a command of unseenWriters that writes: tar -x, an
// unzip that does not only list or test, 7z x, cpio -i, patch…
func unseenWrite(name string, args []string) bool {
	if !unseenWriters[name] {
		return false
	}
	first := ""
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			first = a
			break
		}
	}
	short := func(letters string) bool {
		for i, a := range args {
			if strings.HasPrefix(a, "--") {
				continue
			}
			if strings.HasPrefix(a, "-") || i == 0 {
				if strings.ContainsAny(strings.TrimPrefix(a, "-"), letters) {
					return true
				}
			}
		}
		return false
	}
	switch name {
	case "tar", "bsdtar", "gtar":
		return hasText(args, "--extract", "--get") || short("x")
	case "unzip":
		return !hasText(args, "-l", "-v", "-t", "-Z", "-p", "-z")
	case "7z", "7za", "7zr", "7zz", "unrar", "rar":
		return first == "x" || first == "e"
	case "cpio":
		return hasText(args, "--extract", "--pass-through") || short("ip")
	case "pax":
		return short("r")
	case "ar", "jar":
		return len(args) > 0 && strings.Contains(strings.TrimPrefix(args[0], "-"), "x")
	}
	return true // expand-archive, patch
}

func hasText(args []string, flags ...string) bool {
	for _, a := range args {
		for _, f := range flags {
			if a == f || strings.HasPrefix(f, "--") && strings.HasPrefix(a, f+"=") {
				return true
			}
		}
	}
	return false
}

// inlineInterpreters run code given to them; they are a signal unless their
// first argument is a file of the worktree (the shells with -c are nested
// shells, analysed apart).
var inlineInterpreters = map[string]bool{
	"python": true, "python2": true, "python3": true, "py": true, "node": true, "nodejs": true, "deno": true, "bun": true,
	"ruby": true, "perl": true, "php": true, "lua": true, "rscript": true, "osascript": true, "tsx": true, "ts-node": true,
	"wscript": true, "cscript": true, "mshta": true, "source": true, ".": true,
}

// versionArgs only print the version or the help of an interpreter.
var versionArgs = map[string]bool{"--version": true, "-v": true, "-V": true, "--help": true, "-h": true}

// globalFlagPrograms install outside the worktree with globalFlags.
var (
	globalFlagPrograms = map[string]bool{"npm": true, "pnpm": true, "yarn": true, "bun": true, "uv": true}
	globalFlags        = map[string]bool{"-g": true, "--global": true, "--location=global": true, "--system": true}
)

// programEnvBuiltins set a variable for the commands that follow: one of
// programEnv changes the program they run.
var programEnvBuiltins = map[string]bool{"export": true, "declare": true, "typeset": true, "readonly": true, "local": true, "setx": true}

var (
	scpHost  = regexp.MustCompile(`^[^@/\s]+@([A-Za-z0-9.-]+):`)
	ipv4Host = regexp.MustCompile(`^(\d{1,3}(?:\.\d{1,3}){3})(?::\d+)?(?:/.*)?$`)
	localArg = regexp.MustCompile(`^(localhost)(?::\d+)?(?:/.*)?$`)
)

// hostOf is the host an argument names without a scheme: user@host:path, an
// IPv4 address or localhost (with an optional port). URLs are counted by
// scan.
func hostOf(a string) (string, bool) {
	for _, re := range []*regexp.Regexp{scpHost, ipv4Host, localArg} {
		if m := re.FindStringSubmatch(a); m != nil {
			return strings.ToLower(m[1]), true
		}
	}
	return "", false
}

// floors are the signals that hold over the rows of the table as well: a
// URL or a host of a domain the effective rules do not list, and a global
// install flag of a package manager. The network rows lower themselves only
// for the listed domains, so a floor never raises them.
func (w *riskWalker) floors(c *riskCmd) riskOut {
	var out riskOut
	if net, ok := w.networkArg(c); ok {
		out.raise(net)
	}
	if globalFlagPrograms[c.name] {
		for i, a := range c.args {
			if globalFlags[a] || a == "--location" && i+1 < len(c.args) && c.args[i+1] == "global" {
				out.raise(riskOut{risk: RiskMedium, rule: "sinal de instalação ou configuração do sistema: " + c.name + " " + a})
				break
			}
		}
	}
	return out
}

// networkArg is the network signal of the arguments: a URL whose host the
// effective rules do not list (a list with any domain lists none), or a
// host named without a scheme.
func (w *riskWalker) networkArg(c *riskCmd) (riskOut, bool) {
	for _, a := range c.args {
		host := ""
		if strings.Contains(a, "://") {
			if u, err := url.Parse(a); err == nil {
				host = u.Hostname()
			}
			if host == "" {
				return riskOut{risk: RiskMedium, rule: "sinal de rede: URL que o motor não lê: " + a}, true
			}
		} else if h, ok := hostOf(a); ok {
			host = h
		} else {
			continue
		}
		if !w.inDomains(host) {
			return riskOut{risk: RiskMedium, rule: "sinal de rede: " + host + " fora dos domínios das regras efetivas"}, true
		}
	}
	return riskOut{}, false
}

// signal is the risk signal of a part that matches no row, if any.
func (w *riskWalker) signal(c *riskCmd, ws []word, st *shellState) (riskOut, bool) {
	sig := func(group, what string) (riskOut, bool) {
		return riskOut{risk: RiskMedium, rule: "sinal de " + group + ": " + what}, true
	}
	if f := w.floors(c); f.set() {
		return f, true
	}
	if g, ok := signalPrograms[c.name]; ok {
		return sig(g, c.name)
	}
	for p, g := range signalPrefixes {
		if strings.HasPrefix(c.name, p) {
			return sig(g, c.name)
		}
	}
	for _, r := range signalPatterns {
		if c.matches(r.pattern, true) {
			return sig(r.group, r.pattern)
		}
	}
	if unseenWrite(c.name, c.args) {
		return sig(groupUnseenWrite, c.text)
	}
	for _, a := range c.args {
		if a == "--delete" || strings.HasPrefix(a, "--delete-") || strings.HasPrefix(a, "--delete=") {
			return sig("apagar", c.name+" "+a)
		}
	}
	if inlineInterpreters[c.name] {
		args := ws[1:]
		switch {
		case len(args) == 1 && versionArgs[args[0].text]:
		case len(args) == 0 || strings.HasPrefix(args[0].text, "-") || args[0].glob || !w.inWorktree(args[0], st):
			return sig("interpretador com código", c.name+" sem um arquivo do worktree")
		}
	}
	if programEnvBuiltins[c.name] {
		if c.name == "setx" && len(c.args) > 0 && programEnv[strings.ToUpper(c.args[0])] {
			return sig("leitura do shell", c.args[0]+" muda o programa que roda")
		}
		for _, a := range c.args {
			if n, _, ok := strings.Cut(a, "="); ok && programEnv[strings.ToUpper(strings.TrimPrefix(n, "env:"))] {
				return sig("leitura do shell", n+" muda o programa que roda")
			}
		}
	}
	return riskOut{}, false
}

// valuePaths are the paths after the = of the arguments name=value (an
// option --x=value is a path use already): DESTDIR=/usr/local, out=/etc/x.
func valuePaths(ws []word) []word {
	var out []word
	for _, a := range ws {
		if strings.HasPrefix(a.text, "-") {
			continue
		}
		if _, v, ok := strings.Cut(a.text, "="); ok && looksLikePath(v) && !strings.Contains(v, "://") {
			nw := a
			nw.text, nw.tilde = v, strings.HasPrefix(v, "~")
			out = append(out, nw)
		}
	}
	return out
}
