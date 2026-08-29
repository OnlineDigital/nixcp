package state

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

const (
	dirMode  fs.FileMode = 0700
	fileMode fs.FileMode = 0600
)

type Store struct{ Root string }

func NewStore(home string) *Store   { return &Store{Root: filepath.Join(home, ".nixcp")} }
func (s *Store) ConfigPath() string { return filepath.Join(s.Root, "config.yaml") }
func (s *Store) SitesPath() string  { return filepath.Join(s.Root, "sites") }
func (s *Store) Initialize(cfg ConfigSnapshot) error {
	cfg.Canonicalize()
	if err := ValidateConfig(cfg); err != nil {
		return err
	}
	for _, d := range []string{s.Root, s.SitesPath(), filepath.Join(s.Root, "generated"), filepath.Join(s.Root, "shell"), filepath.Join(s.Root, "backups"), filepath.Join(s.Root, "transactions"), filepath.Join(s.Root, "nginx-templates")} {
		if err := ensurePrivateDir(d); err != nil {
			return err
		}
	}
	return s.WriteSnapshot(Snapshot{Config: cfg})
}
func (s *Store) Load() (Snapshot, error) {
	if err := checkRegularPrivate(s.ConfigPath()); err != nil {
		return Snapshot{}, err
	}
	raw, err := os.ReadFile(s.ConfigPath())
	if err != nil {
		return Snapshot{}, err
	}
	cfg, err := NormalizeAndValidateConfig(raw)
	if err != nil {
		return Snapshot{}, err
	}
	entries, err := os.ReadDir(s.SitesPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Snapshot{Config: cfg}, nil
		}
		return Snapshot{}, err
	}
	var sites []SiteConfig
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".yaml" {
			continue
		}
		path := filepath.Join(s.SitesPath(), e.Name())
		if err := checkRegularPrivate(path); err != nil {
			return Snapshot{}, err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return Snapshot{}, err
		}
		site, err := NormalizeAndValidateSite(b)
		if err != nil {
			return Snapshot{}, err
		}
		sites = append(sites, site)
	}
	sort.Slice(sites, func(i, j int) bool { return sites[i].ID < sites[j].ID })
	snap := Snapshot{Config: cfg, Sites: sites}
	return snap, snap.Validate()
}
func (s *Store) WriteSnapshot(snap Snapshot) error {
	if err := snap.Validate(); err != nil {
		return err
	}
	if err := ensurePrivateDir(s.Root); err != nil {
		return err
	}
	if err := ensurePrivateDir(s.SitesPath()); err != nil {
		return err
	}
	cfg, err := marshalCanonical(snap.Config)
	if err != nil {
		return err
	}
	if err := atomicWrite(s.ConfigPath(), cfg); err != nil {
		return err
	}
	desired := map[string]bool{}
	for _, site := range snap.Sites {
		b, err := marshalCanonical(site)
		if err != nil {
			return err
		}
		name := site.ID + ".yaml"
		desired[name] = true
		if err := atomicWrite(filepath.Join(s.SitesPath(), name), b); err != nil {
			return err
		}
	}
	entries, _ := os.ReadDir(s.SitesPath())
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".yaml" && !desired[e.Name()] {
			if err := os.Remove(filepath.Join(s.SitesPath(), e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}
func ensurePrivateDir(path string) error {
	info, err := os.Lstat(path)
	if err == nil {
		if !info.IsDir() || info.Mode()&0077 != 0 {
			return newStateError("unsafe_state_path", "managed directory must be private and not symlink", nil)
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.MkdirAll(path, dirMode)
}
func checkRegularPrivate(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&0077 != 0 {
		return newStateError("unsafe_state_path", "managed file must be regular and mode 0600", nil)
	}
	return nil
}
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := ensurePrivateDir(dir); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".nixcp-")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(fileMode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
func marshalCanonical(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("encode YAML: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
