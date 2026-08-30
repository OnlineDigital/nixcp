package main

import (
	"context"
	"fmt"
	"os"

	"github.com/nixcp/nixcp/internal/command"
)

var (
	Version = ""
	Commit  = ""
	BuiltAt = ""
)

func main() {
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
