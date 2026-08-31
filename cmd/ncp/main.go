package main

import (
	"context"
	"fmt"
	"os"
	"syscall"

	"github.com/nixcp/nixcp/internal/command"
)

var (
	Version = ""
	Commit  = ""
	BuiltAt = ""
)

func main() {
	// NixCP creates state, generated configuration, locks, journals, and local
	// PHP markers. Keep every process-created object private by default; the
	// narrow helpers used by library paths apply the same policy when main is
	// not the entry point.
	syscall.Umask(0077)
	version := Version
	if version == "" {
		version = "0.0.0"
	}

	rootApp, err := command.New(
		context.Background(),
		command.WithBuildMetadata(command.BuildMetadata{
			Version: version,
			Commit:  Commit,
			BuiltAt: BuiltAt,
		}),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(9)
		return
	}

	os.Exit(rootApp.Execute())
}
