package classes

import "strings"

// Write targets that a command does not name as an argument: the implicit
// output of a decompressor (gunzip X.gz writes X), the destination tree of
// an archive extraction (tar -C, unzip -d, 7z -o, dpkg-deb -x) and of the
// Windows copy tools (xcopy, robocopy), and the link of fsutil hardlink.
// Routing them through argPath lets the fixed rules deny a write that lands
// on a protected path (docs/classes/permissoes.md#regras-fixas-de-segurança).

// decompressSuffix is the suffix a decompressor strips to name its output.
var decompressSuffix = map[string]string{
	"gunzip": ".gz", "gzip": ".gz", "zcat": ".gz",
	"bunzip2": ".bz2", "bzip2": ".bz2", "bzcat": ".bz2",
	"unxz": ".xz", "xz": ".xz", "xzcat": ".xz",
	"unlzma": ".lzma", "lzma": ".lzma",
	"unzstd": ".zst", "zstd": ".zst", "zstdcat": ".zst",
	"unlz4": ".lz4", "lz4": ".lz4",
	"lzop": ".lzo", "brotli": ".br", "uncompress": ".Z", "compress": ".Z",
}

// decompressDefault decompress without an explicit -d.
var decompressDefault = map[string]bool{
	"gunzip": true, "bunzip2": true, "unxz": true, "unlzma": true, "unzstd": true,
	"unlz4": true, "uncompress": true, "zcat": true, "bzcat": true, "xzcat": true, "zstdcat": true,
}

// decompressorOutputs are the files a decompressor writes: the input
// without its suffix (or with the -S/--suffix one). Nothing is written when
// the output goes to stdout (-c/--stdout, the *cat forms) or the command
// only lists or tests (-l/-t).
func decompressorOutputs(cn string, args []word) []word {
	suf, ok := decompressSuffix[cn]
	if !ok {
		return nil
	}
	decompress := decompressDefault[cn] || hasShortFlag(args, 'd') || hasFlag(args, "--decompress", "--uncompress")
	if !decompress {
		return nil
	}
	if strings.HasSuffix(cn, "cat") {
		return nil // always to stdout
	}
	if hasShortFlag(args, 'c', 'l', 't') || hasFlag(args, "--stdout", "--to-stdout", "-c", "--list", "--test") {
		return nil
	}
	if s := flagValue(args, "-S", "--suffix"); s != nil {
		suf = s.text
	}
	var out []word
	for _, w := range operandWords(args, map[string]bool{
		"-S": true, "--suffix": true, "-b": true, "--blocksize": true,
		"-p": true, "--processes": true, "-T": true, "--threads": true, "-o": true, "--output": true,
	}) {
		if w.text == "-" || w.text == "" {
			continue
		}
		base := strings.TrimSuffix(w.text, suf)
		if base == w.text || base == "" {
			continue // no known suffix to strip, so the output is unknown
		}
		nw := w
		nw.text = base
		out = append(out, nw)
	}
	return out
}

// extractDest is the destination an archive extraction or a Windows copy
// tool writes under, as a recursive write. A link tool (fsutil hardlink)
// writes the link and its target.
func extractDest(cn string, args []word) []pathUse {
	rec := use{write: true, recursive: true}
	one := func(w *word) []pathUse {
		if w == nil {
			return nil
		}
		return []pathUse{{*w, rec}}
	}
	switch cn {
	case "tar", "bsdtar", "gtar", "star":
		return one(dirFlag(args, "-C", "--directory", "--cd"))
	case "unzip":
		return one(flagValue(args, "-d"))
	case "7z", "7za", "7zr", "7zz":
		// -o<dir> is attached (7z x a.7z -o/path).
		for _, a := range args {
			if v, ok := strings.CutPrefix(a.text, "-o"); ok && v != "" {
				w := a
				w.text = v
				return []pathUse{{w, rec}}
			}
		}
	case "unsquashfs", "cabextract":
		return one(dirFlag(args, "-d", "--dest", "--directory"))
	case "dpkg-deb":
		if hasShortFlag(args, 'x', 'X') || hasFlag(args, "--extract", "--raw-extract", "--vextract") {
			// dpkg-deb -x <deb> <dir>
			o := operandWords(args, nil)
			if len(o) >= 2 {
				return []pathUse{{o[1], rec}}
			}
		}
	case "xcopy":
		// xcopy <src> <dest> [options]
		o := operandWords(args, nil)
		if len(o) >= 2 {
			return []pathUse{{o[1], rec}}
		}
	case "robocopy":
		// robocopy <src> <dest> [files] [options]
		o := operandWords(args, nil)
		if len(o) >= 2 {
			return []pathUse{{o[1], rec}}
		}
	case "fsutil":
		// fsutil hardlink create <link> <target>
		o := operandWords(args, nil)
		if len(o) >= 3 && strings.EqualFold(o[0].text, "hardlink") && strings.EqualFold(o[1].text, "create") {
			uses := []pathUse{{o[2], use{write: true}}}
			if len(o) >= 4 {
				uses = append(uses, pathUse{o[3], use{write: true}})
			}
			return uses
		}
	}
	return nil
}

// dirFlag is the value of a short or long directory flag, combined forms
// included (tar -C dir, tar --directory=dir).
func dirFlag(args []word, names ...string) *word {
	if w := flagValue(args, names...); w != nil {
		return w
	}
	for _, n := range names {
		if len(n) == 2 && n[0] == '-' && n[1] != '-' {
			if w := shortFlagValue(args, n[1]); w != nil {
				return w
			}
		}
	}
	return nil
}

// targetDirWord is the -t/--target-directory of cp, mv, install and ln,
// combined short flags included (ln -st dir x).
func targetDirWord(args []word) *word {
	if w := flagValue(args, "-t", "--target-directory"); w != nil {
		return w
	}
	return shortFlagValue(args, 't')
}

// shortFlagValue is the value of a value-taking short flag even when it is
// combined with others (-st dir, -S.bak): the rest of the group after the
// letter, or the next word when the letter ends the group.
func shortFlagValue(args []word, letter byte) *word {
	for i := 0; i < len(args); i++ {
		t := args[i].text
		if len(t) < 2 || t[0] != '-' || t[1] == '-' {
			continue
		}
		body := t[1:]
		j := strings.IndexByte(body, letter)
		if j < 0 {
			continue
		}
		if j+1 < len(body) {
			w := args[i]
			w.text = body[j+1:]
			return &w
		}
		if i+1 < len(args) {
			return &args[i+1]
		}
		return nil
	}
	return nil
}

// projectHookScripts are the project's git-hook scripts that git runs on an
// operation: husky (.husky/), a git-hooks folder (.githooks/, a common
// core.hooksPath), and .claude/hooks/ (the provider hooks the Mestre's own
// dispatcher is configured through). The agent's own git operations (and
// the dispatcher) run as mestre-agente, so a script planted here runs with
// the character's reach on the next operation; a write to one is a fixed
// deny, like .git/hooks (already in protectedWrite). A file write has no
// middle "ask" tier (it is allow or deny), so the fixed deny is how the
// rule is at least medio. Recorded in docs/classes/permissoes.md.
var projectHookScripts = []string{
	"**/.husky", "**/.husky/**",
	"**/.githooks", "**/.githooks/**",
	"**/.claude/hooks", "**/.claude/hooks/**",
}

func init() {
	// Add the project hook scripts to the fixed protected-write rules; a
	// new file does this to avoid editing the list in place.
	protectedWrite = append(protectedWrite, projectHookScripts...)
	holdsProtected[".husky"] = true
	holdsProtected[".githooks"] = true
	holdsProtected[".claude/hooks"] = true
}
