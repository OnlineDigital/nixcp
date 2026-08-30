package command

import (
	"context"
	"testing"
)

func TestRootRejectsSudoEnvironment(t *testing.T) {
	t.Setenv("SUDO_UID", "1234")
	root, err := NewRootCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	root.SetArgs([]string{"version"})
	if err := root.Execute(); err == nil {
		t.Fatal("expected sudo environment rejection")
	}
}
