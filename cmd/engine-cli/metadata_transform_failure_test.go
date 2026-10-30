package main

import (
	"bytes"
	"testing"
)

func TestMetadataTransformFailureWritesNoOutput(t *testing.T) {
	library := createTestLibrary(t)
	for _, args := range [][]string{
		{"capitalize", "999999", "--fields", "title", "--recipe", "upper", "--dry-run"},
		{"find-replace", "999999", "--fields", "title", "--find", "x", "--replacement", "y", "--dry-run"},
	} {
		for _, asJSON := range []bool{false, true} {
			t.Run(args[0]+map[bool]string{false: "/human", true: "/json"}[asJSON], func(t *testing.T) {
				command := newRootCommand()
				var out, diagnostics bytes.Buffer
				command.SetOut(&out)
				command.SetErr(&diagnostics)
				commandArgs := append([]string{"-l", library, "--no-backup", "track", "batch"}, args...)
				if asJSON {
					commandArgs = append(commandArgs, "--json")
				}
				command.SetArgs(commandArgs)
				if err := command.Execute(); err == nil || out.Len() != 0 {
					t.Fatalf("failed review stdout=%q stderr=%q error=%v", out.String(), diagnostics.String(), err)
				}
			})
		}
	}
}
