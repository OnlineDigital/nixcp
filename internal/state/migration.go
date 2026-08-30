package state

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// MigrationPolicy is deliberately explicit: readers never alter the YAML source
// of truth. A mutating caller may use MigrateForWrite before staging a snapshot.
// v1 is the first supported format, so there is currently no predecessor to
// migrate. Future migrations must be registered here and be idempotent.
type MigrationPolicy struct {
	Current int
}

func DefaultMigrationPolicy() MigrationPolicy {
	return MigrationPolicy{Current: supportedSchemaVersion}
}

func (p MigrationPolicy) Check(version int) error {
	if version > p.Current {
		return newStateError("unsupported_schema", fmt.Sprintf("schemaVersion %d is newer than this NixCP binary", version), nil)
	}
	if version < 1 {
		return newStateError("unsupported_schema", "schemaVersion must be a positive supported version", nil)
	}
	if version != p.Current {
		return newStateError("migration_required", fmt.Sprintf("schemaVersion %d requires a migration to %d", version, p.Current), nil)
	}
	return nil
}

// BackupForMigration makes a private, timestamped copy before a future migration
// changes any state. It is intentionally not called by Load.
func (s *Store) BackupForMigration() (string, error) {
	stamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	dst := filepath.Join(s.Root, "backups", "pre-migration-"+stamp)
	if err := ensurePrivateDir(filepath.Dir(dst)); err != nil {
		return "", err
	}
	if err := copyPrivateTree(s.Root, dst, map[string]bool{"backups": true}); err != nil {
		return "", err
	}
	return dst, nil
}

func copyPrivateTree(src, dst string, skip map[string]bool) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == src {
			return os.Mkdir(dst, dirMode)
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if skip[rel] || (len(rel) > 0 && skip[rel[:firstPathElementLen(rel)]]) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() && !info.IsDir() {
			return newStateError("unsafe_state_path", "migration source contains non-regular file", nil)
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.Mkdir(target, dirMode)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return atomicWrite(target, b)
	})
}
func firstPathElementLen(s string) int {
	for i, r := range s {
		if r == filepath.Separator {
			return i
		}
	}
	return len(s)
}
