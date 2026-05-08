package main

import (
	"fmt"
	"os"

	"github.com/nicolasacchi/chx/internal/commands"
)

var version = "dev"

func main() {
	commands.SetVersion(version)
	if err := commands.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(exitCode(err))
	}
}

func exitCode(err error) int {
	if ec, ok := err.(interface{ ExitCode() int }); ok {
		return ec.ExitCode()
	}
	return 1
}
