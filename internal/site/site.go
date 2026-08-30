// Package site holds cross-command site concerns: shared site rules (safe
// document roots, world-writable refusal) and the site health check contract
// used by link transactions and by status/doctor diagnostics.
package site

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MaxSymlinkHops bounds path canonicalization to avoid unbounded loops.
const MaxSymlinkHops = 40

// DocumentRootError describes why a proposed document root was refused.
type DocumentRootError struct {
	Path   string
	Reason string
}

func (e *DocumentRootError) Error() string {
	return fmt.Sprintf("%s: %s", e.Path, e.Reason)
}

// CanonicalizePath resolves symlinks and returns the absolute physical path.
func CanonicalizePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", &DocumentRootError{Path: path, Reason: "path is empty"}
	}
	cur, err := filepath.Abs(path)
	if err != nil {
		return "", &DocumentRootError{Path: path, Reason: "cannot make path absolute"}
	}
	for hops := 0; hops < MaxSymlinkHops; hops++ {
		info, err := os.Lstat(cur)
		if err != nil {
			return "", &DocumentRootError{Path: cur, Reason: "cannot stat path"}
		}
		if info.Mode()&os.ModeSymlink == 0 {
			return cur, nil
		}
		target, err := os.Readlink(cur)
		if err != nil {
			return "", &DocumentRootError{Path: cur, Reason: "cannot read symlink"}
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(cur), target)
		}
		cur = target
	}
	return "", &DocumentRootError{Path: cur, Reason: "too many symlink hops"}
}

// RefuseWorldWritable rejects a document root when any path component from
// the canonical parent up to the filesystem root is world-writable. A
// world-writable ancestor lets any local user swap the code that Nginx
// serves, so linking such a tree is refused outright.
//
// RefuseWorldWritable rejects a document root when any path component
// from the root itself up to the filesystem root is world-writable without
// the sticky bit. A world-writable ancestor lets any local user swap the
// code that Nginx serves; the sticky bit (as on /tmp) restores ownership
// semantics enough for staging, so sticky parents are tolerated the same
// way the original link-path rules did.
func RefuseWorldWritable(root string) error {
	canonical, err := CanonicalizePath(root)
	if err != nil {
		return err
	}
	cur := canonical
	for {
		info, statErr := os.Lstat(cur)
		if statErr != nil {
			return &DocumentRootError{Path: cur, Reason: "cannot stat path"}
		}
		if !info.IsDir() {
			return &DocumentRootError{Path: cur, Reason: "not a directory"}
		}
		if info.Mode().Perm()&0002 != 0 && info.Mode()&os.ModeSticky == 0 {
			return &DocumentRootError{Path: cur, Reason: "path is world-writable"}
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return nil
		}
		cur = parent
	}
}
