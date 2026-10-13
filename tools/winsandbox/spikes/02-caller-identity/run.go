//go:build windows

package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type tcase struct {
	name   string
	term   string // "" = run from outside any terminal
	line   string
	outer  []string          // outside: command to run here
	expect map[string]string // label -> terminal of the true origin ("" = must be refused)
	maybe  []string          // labels that may not appear (a spawn that should fail)
	wait   time.Duration
}

func cases() []tcase {
	p := exePath
	return []tcase{
		{name: "direct", term: "T1", line: p + " probe -label direct", expect: map[string]string{"direct": "T1"}},
		{name: "cmd /c", term: "T1", line: "cmd /c " + p + " probe -label cmd-c", expect: map[string]string{"cmd-c": "T1"}},
		{name: "powershell -c", term: "T1", line: `powershell -NoProfile -c "& '` + p + `' probe -label ps-c"`, expect: map[string]string{"ps-c": "T1"}},
		{name: "own nested job", term: "T1", line: p + " probe -label nestjob -nestjob", expect: map[string]string{"nestjob": "T1"}},
		{name: "orphan: start /b from a cmd that exits", term: "T1", line: `cmd /c start "" /b ` + p + " probe -label orphan-start-b -wait-parent", expect: map[string]string{"orphan-start-b": "T1"}},
		{name: "orphan: Start-Process from a powershell that exits", term: "T1", line: `powershell -NoProfile -c "Start-Process -NoNewWindow -FilePath '` + p + `' -ArgumentList 'probe','-label','orphan-ps','-wait-parent'"`, expect: map[string]string{"orphan-ps": "T1"}},
		{name: "breakaway, job without BREAKAWAY_OK", term: "T1", line: p + " probe -label breakaway-t1 -breakaway", expect: map[string]string{"breakaway-t1": "T1", "breakaway-t1-child": "T1"}, maybe: []string{"breakaway-t1-child"}},
		{name: "process created by WMI (Win32_Process.Create)", term: "T1", line: `powershell -NoProfile -c "Invoke-CimMethod -ClassName Win32_Process -MethodName Create -Arguments @{CommandLine='` + p + ` probe -label wmi'} | Out-Null"`, expect: map[string]string{"wmi": "T1"}},
		{name: "direct in T2", term: "T2", line: p + " probe -label direct-t2", expect: map[string]string{"direct-t2": "T2"}},
		{name: "breakaway, job with BREAKAWAY_OK", term: "T2", line: p + " probe -label breakaway-t2 -breakaway", expect: map[string]string{"breakaway-t2": "T2", "breakaway-t2-child": "T2"}},
		{name: "breakaway + orphan, job with BREAKAWAY_OK", term: "T2", line: p + " probe -label obk-t2 -breakaway -orphan-child", expect: map[string]string{"obk-t2": "T2", "obk-t2-child": "T2"}},
		{name: "outside: Administrator console", outer: []string{p, "probe", "-label", "outside-admin"}, expect: map[string]string{"outside-admin": ""}},
		{name: "outside: mestre-agente in another job (spikesvc via launcher)", outer: []string{filepath.Join(binDir, "spikesvc.exe"), "open", "-svc", "launcher", "-deadline", "60s", "--", p, "probe", "-label", "outside-agent"}, expect: map[string]string{"outside-agent": ""}},
	}
}

func ctl(c control) (controlResp, error) {
	var r controlResp
	if err := callJSON(controlPipe, c, &r); err != nil {
		return r, err
	}
	if r.Error != "" {
		return r, errors.New(r.Error)
	}
	return r, nil
}

func verdict(expect string, m methodResult) string {
	switch {
	case m.Terminal == expect && expect == "":
		return "refused (correct)"
	case m.Terminal == expect:
		return "ok " + expect
	case m.Terminal == "":
		return "refused"
	}
	return "WRONG: " + m.Terminal
}

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	dacl := fs.Bool("dacl", false, "add the service to the token's default DACL (PROCESS_QUERY_LIMITED_INFORMATION|SYNCHRONIZE)")
	outDir := fs.String("out", `C:\winsandbox\resultados\rodada-2`, "folder for the results")
	_ = fs.Parse(args)
	mode := "dacl-padrao"
	if *dacl {
		mode = "dacl-com-servico"
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		return err
	}
	r, err := ctl(control{Op: "setup", DACL: *dacl})
	for _, l := range r.Log {
		fmt.Println("setup:", l)
	}
	if err != nil {
		return err
	}
	defer ctl(control{Op: "teardown"})

	var b strings.Builder
	fmt.Fprintf(&b, "# callerid run, %s, %s\n\n", mode, time.Now().Format(time.RFC3339))
	for _, l := range r.Log {
		fmt.Fprintf(&b, "- setup: %s\n", l)
	}
	b.WriteString("\n| case | label | expected | tree (toolhelp) | tree (ntqsi) | job (IsProcessInJob) | job (PID list) | note |\n|---|---|---|---|---|---|---|---|\n")
	all := map[string]*identity{}
	for _, c := range cases() {
		start := time.Now()
		var outer []byte
		if c.term != "" {
			if _, err := ctl(control{Op: "type", Term: c.term, Line: c.line}); err != nil {
				return err
			}
		} else {
			outer, _ = exec.Command(c.outer[0], c.outer[1:]...).CombinedOutput()
		}
		var must []string
		for l := range c.expect {
			if !slices.Contains(c.maybe, l) {
				must = append(must, l)
			}
		}
		wait := c.wait
		if wait == 0 {
			wait = 40 * time.Second
		}
		w, err := ctl(control{Op: "wait", Labels: must, TimeoutS: wait.Seconds()})
		if err != nil {
			return err
		}
		if len(c.maybe) > 0 {
			// The maybe-labels come from a child started before the parent
			// called, so a short bounded wait is enough.
			m, _ := ctl(control{Op: "wait", Labels: c.maybe, TimeoutS: 3})
			for k, v := range m.Results {
				w.Results[k] = v
			}
		}
		fmt.Printf("== %s (%s, waited %v, deadline %v)\n", c.name, c.term, time.Since(start).Round(time.Millisecond), wait)
		if len(outer) > 0 {
			fmt.Printf("   outside output: %s\n", strings.TrimSpace(string(outer)))
		}
		for _, l := range slices.Sorted(maps.Keys(c.expect)) {
			id, ok := w.Results[l]
			if !ok {
				line := fmt.Sprintf("| %s | %s | %s | - | - | - | - | did not call (in %v) |\n", c.name, l, show(c.expect[l]), wait)
				fmt.Print("   " + line)
				b.WriteString(line)
				continue
			}
			all[l] = id
			line := fmt.Sprintf("| %s | %s | %s | %s | %s | %s | %s | %s |\n", c.name, l, show(c.expect[l]),
				verdict(c.expect[l], id.TreeToolhelp), verdict(c.expect[l], id.TreeNtqsi),
				verdict(c.expect[l], id.JobInJob), verdict(c.expect[l], id.JobPIDList), strings.ReplaceAll(id.Note, "|", "/"))
			fmt.Print("   " + line)
			b.WriteString(line)
		}
	}
	b.WriteString("\n## Details (errors, tree paths, mean cost per identification)\n\n")
	cost := map[string][]float64{}
	for _, l := range slices.Sorted(maps.Keys(all)) {
		id := all[l]
		fmt.Fprintf(&b, "- **%s** pid %d %s open_err=%q\n", l, id.PID, id.Image, id.OpenErr)
		for _, m := range []struct {
			n string
			r methodResult
		}{{"tree_toolhelp", id.TreeToolhelp}, {"tree_ntqsi", id.TreeNtqsi}, {"job_isprocessinjob", id.JobInJob}, {"job_pidlist", id.JobPIDList}} {
			fmt.Fprintf(&b, "  - %s: terminal=%q err=%q path=%v cost=%.1f µs (%d runs)\n", m.n, m.r.Terminal, m.r.Err, m.r.Path, m.r.MeanMicros, m.r.Runs)
			cost[m.n] = append(cost[m.n], m.r.MeanMicros)
		}
	}
	b.WriteString("\n## Cost per method over all cases (µs: min / median / max)\n\n")
	for _, n := range slices.Sorted(maps.Keys(cost)) {
		c := slices.Sorted(slices.Values(cost[n]))
		line := fmt.Sprintf("- %s: %.1f / %.1f / %.1f\n", n, c[0], c[len(c)/2], c[len(c)-1])
		fmt.Print(line)
		b.WriteString(line)
	}
	for _, t := range []string{"T1", "T2"} {
		if o, err := ctl(control{Op: "output", Term: t}); err == nil {
			fmt.Fprintf(&b, "\n## Terminal %s output\n\n```\n%s\n```\n", t, strings.TrimSpace(o.Output))
		}
	}
	js, _ := json.MarshalIndent(all, "", "  ")
	_ = os.WriteFile(filepath.Join(*outDir, "b-"+mode+".json"), js, 0o644)
	md := filepath.Join(*outDir, "b-"+mode+".md")
	if err := os.WriteFile(md, []byte(b.String()), 0o644); err != nil {
		return err
	}
	fmt.Println("results:", md)
	return nil
}

func show(t string) string {
	if t == "" {
		return "refuse"
	}
	return t
}
