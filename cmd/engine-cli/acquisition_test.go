package main

import (
	"bytes"
	"testing"
)

func TestNativeAcquisitionCommands(t *testing.T) {
	for _, args := range [][]string{{"track", "fetch", "--help"}, {"track", "candidates", "--help"}, {"track", "verify", "--help"}, {"track", "tags", "update", "--help"}, {"track", "artwork", "embed", "--help"}, {"mix", "fetch", "--help"}, {"mix", "inspect", "--help"}, {"mix", "candidates", "--help"}, {"ai", "list", "--help"}, {"ai", "inspect", "--help"}, {"tool", "dependencies", "install", "--help"}, {"staging", "retry", "--help"}} {
		root := newRootCommand()
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
}
