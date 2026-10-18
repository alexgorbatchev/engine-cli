package main

import (
	"fmt"
	"os"

	"github.com/alexgorbatchev/engine-cli/internal/agent"
)

func main() {
	if err := newRootCommand().Execute(); err != nil {
		if agent.IsAgentMode() {
			fmt.Fprintf(os.Stderr, "ERR: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
		}
		os.Exit(1)
	}
}
