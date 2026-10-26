#!/bin/sh
# Shared by the pre-commit and pre-push hooks (docs/desenvolvimento/ambiente.md,
# "Ganchos de git"). Sourced, never run.
#
# with_suite_lock runs a command while holding one lock for the whole machine,
# so suites started by several clones, worktrees or sessions run one at a time
# instead of fighting for the CPU: alone, the fast suite fits its budget; with
# four at once none does. The wait is not part of any budget, because the
# runner starts counting after it. Without flock (Git for Windows ships none)
# the command just runs.
#
# passed/mark remember that a tree already went through a suite today, so
# `git commit --amend` or a repeated push of the same commit does not run it
# again. The day is part of the key because the quarantine expires by date.
# MESTRE_FORCE_SUITE=1 ignores the memory.

with_suite_lock() {
	if ! command -v flock >/dev/null 2>&1; then
		"$@"
		return
	fi
	lock="${MESTRE_SUITE_LOCK:-${TMPDIR:-/tmp}/mestre-suite.lock}"
	exec 9>>"$lock"
	if ! flock -n 9; then
		echo "hook: another suite is running on this machine, waiting for it ($lock)"
		flock 9
	fi
	"$@"
}

# passed KEY: true when KEY went through a suite today.
passed() {
	[ -z "${MESTRE_FORCE_SUITE:-}" ] && [ -f "$1.$(date +%Y-%m-%d)" ]
}

# mark KEY: remember that KEY passed today and forget the older marks.
mark() {
	mkdir -p "$(dirname "$1")"
	: >"$1.$(date +%Y-%m-%d)"
	find "$(dirname "$1")" -type f -mtime +2 -exec rm -f {} + 2>/dev/null || true
}

# web_copy ROOT: copies web/ of the current folder (a snapshot, inside the git
# directory) to a temporary folder and prints its path. Vitest does not work in
# a path with a ".git" folder in it, so the web side runs from that copy,
# borrowing the node_modules of the working tree ROOT. The testrun built from
# the snapshot is put next to it.
web_copy() {
	exe="$(go env GOEXE)"
	wsnap="${TMPDIR:-/tmp}/mestre-web-snapshot-$(printf '%s' "$1" | cksum | cut -d' ' -f1)"
	rm -rf "$wsnap"
	mkdir -p "$wsnap/test"
	cp -R web "$wsnap/web"
	[ ! -f test/quarantine.yaml ] || cp test/quarantine.yaml "$wsnap/test/"
	[ ! -d "$1/web/node_modules" ] || ln -s "$1/web/node_modules" "$wsnap/web/node_modules"
	go build -o "$wsnap/testrun$exe" ./tools/testrun
	printf '%s\n' "$wsnap"
}

# fast_suite ROOT WEB: the whole fast suite of the current folder (a snapshot):
# the Go side, and the web side when WEB is 1.
fast_suite() {
	go run ./tools/testrun fast -only go || return 1
	if [ "$2" = 1 ]; then
		w="$(web_copy "$1")"
		(cd "$w" && "./testrun$(go env GOEXE)" fast -only web) || return 1
	fi
}
