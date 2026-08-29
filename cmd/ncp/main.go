package main

import (
	"context"
	"fmt"
	"os"

	"github.com/nixcp/nixcp/internal/command"
)

func main() {
	rootApp, err := command.New(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(9)
		return
	}

	os.Exit(rootApp.Execute())
}
