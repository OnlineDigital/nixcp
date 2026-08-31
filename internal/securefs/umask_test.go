package securefs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWithPrivateUmaskRestrictsNewFilesAndRestores(t *testing.T) {
	path := filepath.Join(t.TempDir(), "created")
	if err := WithPrivateUmask(func() error {
		return os.WriteFile(path, []byte("x"), 0666)
	}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("mode = %04o, want 0600", got)
	}
}
