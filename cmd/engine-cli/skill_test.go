package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cobrahelptree "github.com/alexgorbatchev/cobra-help-tree/v3"
	"github.com/spf13/cobra"
)

const skillInstructions = "Install the engine-cli skill using the Skills CLI:\n\n  npx skills add alexgorbatchev/engine-cli --skill engine-cli\n\nSkill:\n\n  https://github.com/alexgorbatchev/engine-cli/tree/main/skills/engine-cli\n"

const skillAlert = "ALERT: Agents must read `AGENT=1 engine-cli skill` before using this tool.\n"

func TestSkillInstructions(t *testing.T) {
	// The installed binary must not depend on its working directory or a local skill file.
	t.Chdir(t.TempDir())
	for _, mode := range []string{"0", "1", "true", "yes"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("AGENT", mode)
			cmd := newRootCommand()
			var out, diagnostics bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&diagnostics)
			cmd.SetArgs([]string{"skill"})
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if out.String() != skillInstructions || diagnostics.Len() != 0 {
				t.Fatalf("unexpected skill output stdout=%q stderr=%q", out.String(), diagnostics.String())
			}
		})
	}
}

func TestSkillRejectsArgumentsAndReportsOutputFailure(t *testing.T) {
	for _, args := range [][]string{{"skill", "extra"}, {"skill", "--unknown"}} {
		cmd := newRootCommand()
		cmd.SetArgs(args)
		var out bytes.Buffer
		cmd.SetOut(&out)
		if err := cmd.Execute(); err == nil || out.Len() != 0 {
			t.Fatalf("invalid invocation %v printed instructions or succeeded: %v, %q", args, err, out.String())
		}
	}
	file, err := os.Create(filepath.Join(t.TempDir(), "closed-output"))
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	cmd := newRootCommand()
	cmd.SetOut(file)
	cmd.SetArgs([]string{"skill"})
	if err := cmd.Execute(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("lost native output failure: %v", err)
	}
}

func TestHelpSkillAlertAndVersion(t *testing.T) {
	for _, mode := range []string{"0", "1", "true", "yes"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("AGENT", mode)
			for _, args := range [][]string{{"--help"}, {"track", "cue", "set", "--help"}, {"help", "library"}, {"completion", "bash", "--help"}, {"skill", "--help"}} {
				cmd := newRootCommand()
				var out, diagnostics bytes.Buffer
				cmd.SetOut(&out)
				cmd.SetErr(&diagnostics)
				cmd.SetArgs(args)
				if err := cmd.Execute(); err != nil {
					t.Fatal(err)
				}
				if strings.HasPrefix(out.String(), skillAlert) != (mode != "0") || diagnostics.Len() != 0 {
					t.Fatalf("help %v mode %s: stdout=%q stderr=%q", args, mode, out.String(), diagnostics.String())
				}
			}
			cmd := newRootCommand()
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetArgs([]string{"--version"})
			if err := cmd.Execute(); err != nil || out.String() != version+"\n" {
				t.Fatalf("version contract changed: %q, %v", out.String(), err)
			}
		})
	}
}

func TestMaintainedSkillCoversCommandTree(t *testing.T) {
	root := newRootCommand()
	dir := filepath.Join("..", "..", "skills", root.Name())
	main, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(main), "---\nname: "+root.Name()+"\n") {
		t.Fatal("published skill name does not match the executable/install instructions")
	}
	files, err := filepath.Glob(filepath.Join(dir, "references", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	var reference strings.Builder
	reference.Write(main)
	for _, path := range files {
		link := "references/" + filepath.Base(path)
		if !strings.Contains(string(main), "]("+link+")") {
			t.Fatalf("reference %s is unreachable from the skill", path)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		reference.WriteByte('\n')
		reference.Write(body)
	}
	if omissions := cobrahelptree.SkillOmissions(root, techCatalog, reference.String()); len(omissions) != 0 {
		t.Fatalf("maintained skill drift:\n%s", strings.Join(omissions, "\n"))
	}
	// A newly exposed interface must fail the same native gate, not silently pass
	// because the reference happens to contain a related command or flag name.
	added := &cobra.Command{Use: "undocumented-operation", RunE: func(*cobra.Command, []string) error { return nil }}
	added.Flags().Duration("undocumented-deadline", 0, "Operation deadline")
	root.AddCommand(added)
	omissions := strings.Join(cobrahelptree.SkillOmissions(root, techCatalog, reference.String()), "\n")
	if !strings.Contains(omissions, "command engine-cli undocumented-operation") || !strings.Contains(omissions, "flag --undocumented-deadline") {
		t.Fatalf("new interface escaped the drift gate: %s", omissions)
	}
}

func TestEveryCommandHelpCarriesAgentSkillAlert(t *testing.T) {
	t.Setenv("COLUMNS", "40")
	root := newRootCommand()
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()
	var paths [][]string
	var visit func(*cobra.Command, []string)
	visit = func(cmd *cobra.Command, path []string) {
		paths = append(paths, path)
		for _, child := range cmd.Commands() {
			if !child.Hidden && child.Deprecated == "" {
				visit(child, append(append([]string{}, path...), child.Name()))
			}
		}
	}
	visit(root, nil)
	for _, mode := range []string{"0", "1", "true", "yes"} {
		t.Setenv("AGENT", mode)
		for _, path := range paths {
			cmd := newRootCommand()
			var out, diagnostics bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&diagnostics)
			cmd.SetArgs(append(append([]string{}, path...), "--help"))
			if err := cmd.Execute(); err != nil {
				t.Fatalf("help %v: %v", path, err)
			}
			if out.Len() == 0 || strings.HasPrefix(out.String(), skillAlert) != (mode != "0") || diagnostics.Len() != 0 {
				t.Fatalf("help %v mode %s: stdout=%q stderr=%q", path, mode, out.String(), diagnostics.String())
			}
		}
	}
	// Usage has its own native renderer and must carry the alert too.
	t.Setenv("AGENT", "1")
	var diagnostics bytes.Buffer
	root.SetOut(&diagnostics)
	if err := root.Usage(); err != nil || !strings.HasPrefix(diagnostics.String(), skillAlert) {
		t.Fatalf("agent usage lost alert: %q, %v", diagnostics.String(), err)
	}
}
