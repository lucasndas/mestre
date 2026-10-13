package fakeagent

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"time"

	"go.yaml.in/yaml/v3"
)

// Script says, for each prompt (regular expression), the steps of the turn
// (docs/qualidade/dubles.md#agente-falso-fakeagent):
//
//	prompts:
//	  - match: "^fix (.*)"
//	    steps:
//	      - say: "on it"
//	      - tool: {name: Bash, input: {command: "go test ./..."}}
//	      - edit: {path: "a.txt", content: "new"}
//	      - run: 'mestre done "fixed"'
//	      - wait: 5s
//	      - hang: true
//	      - crash: "boom"
//
// The first matching entry wins. Without a match the agent echoes the
// prompt and runs `mestre done`.
type Script struct {
	Prompts []PromptRule `yaml:"prompts"`
}

// PromptRule is one entry of the script.
type PromptRule struct {
	Match string `yaml:"match"`
	Steps []Step `yaml:"steps"`
	re    *regexp.Regexp
}

// Step is one action; exactly one field is set.
type Step struct {
	Say   *string   `yaml:"say"`
	Tool  *ToolStep `yaml:"tool"`
	Edit  *EditStep `yaml:"edit"`
	Run   *string   `yaml:"run"`
	Wait  *string   `yaml:"wait"`
	Hang  bool      `yaml:"hang"`
	Crash *string   `yaml:"crash"`
	wait  time.Duration
}

// ToolStep asks for a tool, firing the hooks before and after.
type ToolStep struct {
	Name  string         `yaml:"name"`
	Input map[string]any `yaml:"input"`
}

// EditStep writes a file (relative to the working folder) through the
// provider's write tool.
type EditStep struct {
	Path    string `yaml:"path"`
	Content string `yaml:"content"`
}

// ParseScript reads and validates a script.
func ParseScript(b []byte) (*Script, error) {
	var s Script
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true) // an unknown key is an error, as in tarefas.yaml
	if err := dec.Decode(&s); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse script: %w", err)
	}
	for i := range s.Prompts {
		r := &s.Prompts[i]
		re, err := regexp.Compile(r.Match)
		if err != nil {
			return nil, fmt.Errorf("script prompt %d: %w", i, err)
		}
		r.re = re
		for j := range r.Steps {
			if err := r.Steps[j].validate(); err != nil {
				return nil, fmt.Errorf("script prompt %d step %d: %w", i, j, err)
			}
		}
	}
	return &s, nil
}

// LoadScript reads a script file.
func LoadScript(path string) (*Script, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read script: %w", err)
	}
	return ParseScript(b)
}

func (s *Step) validate() error {
	n := 0
	for _, set := range []bool{s.Say != nil, s.Tool != nil, s.Edit != nil, s.Run != nil, s.Wait != nil, s.Hang, s.Crash != nil} {
		if set {
			n++
		}
	}
	if n != 1 {
		return errors.New("a step has exactly one of say, tool, edit, run, wait, hang, crash")
	}
	if s.Wait != nil {
		d, err := time.ParseDuration(*s.Wait)
		if err != nil {
			return fmt.Errorf("wait: %w", err)
		}
		s.wait = d
	}
	if s.Tool != nil && s.Tool.Name == "" {
		return errors.New("tool: name is required")
	}
	if s.Edit != nil && s.Edit.Path == "" {
		return errors.New("edit: path is required")
	}
	return nil
}

// stepsFor returns the steps for prompt, or nil when nothing matches.
func (s *Script) stepsFor(prompt string) []Step {
	if s == nil {
		return nil
	}
	for _, r := range s.Prompts {
		if r.re.MatchString(prompt) {
			return r.Steps
		}
	}
	return nil
}
