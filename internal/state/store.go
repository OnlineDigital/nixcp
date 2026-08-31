package state

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/nixcp/nixcp/internal/nginxsnippet"
	"github.com/nixcp/nixcp/internal/securefs"
	"gopkg.in/yaml.v3"
)

const (
	dirMode        fs.FileMode = 0700
	fileMode       fs.FileMode = 0600
	maxSiteEntries             = 1_000
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
	for _, d := range s.managedDirs() {
		if err := ensurePrivateDir(d); err != nil {
			return err
		}
	}
	return s.WriteSnapshot(Snapshot{Config: cfg})
}

func (s *Store) managedDirs() []string {
	return []string{s.Root, s.SitesPath(), filepath.Join(s.Root, "generated"), filepath.Join(s.Root, "shell"), filepath.Join(s.Root, "transactions"), filepath.Join(s.Root, "nginx-templates")}
}

// Load is read-only. It validates every managed state file before returning a
// snapshot and never canonicalizes files on disk.
func (s *Store) Load() (Snapshot, error) {
	if err := checkPrivateDir(s.Root); err != nil {
		return Snapshot{}, err
	}
	if err := checkRegularPrivate(s.ConfigPath()); err != nil {
		return Snapshot{}, err
	}
	raw, err := readPrivateRegular(s.ConfigPath(), os.Getuid(), maxYAMLBytes)
	if err != nil {
		return Snapshot{}, err
	}
	cfg, err := NormalizeAndValidateConfig(raw)
	if err != nil {
		return Snapshot{}, err
	}
	if err := checkOwnedBy(s.Root, cfg.Owner.UID); err != nil {
		return Snapshot{}, err
	}
	if err := checkOwnedBy(s.ConfigPath(), cfg.Owner.UID); err != nil {
		return Snapshot{}, err
	}
	if err := checkPrivateDir(s.SitesPath()); err != nil {
		return Snapshot{}, err
	}
	entries, err := os.ReadDir(s.SitesPath())
	if err != nil {
		return Snapshot{}, err
	}
	if len(entries) > maxSiteEntries {
		return Snapshot{}, newStateError("unsafe_state_path", "sites directory has too many entries", nil)
	}
	sites := make([]SiteConfig, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".yaml" {
			return Snapshot{}, newStateError("unsafe_state_path", "sites directory contains an unmanaged entry", nil)
		}
		path := filepath.Join(s.SitesPath(), e.Name())
		if err := checkRegularPrivate(path); err != nil {
			return Snapshot{}, err
		}
		if err := checkOwnedBy(path, cfg.Owner.UID); err != nil {
			return Snapshot{}, err
		}
		b, err := readPrivateRegular(path, cfg.Owner.UID, maxYAMLBytes)
		if err != nil {
			return Snapshot{}, err
		}
		site, err := NormalizeAndValidateSite(b)
		if err != nil {
			return Snapshot{}, err
		}
		// Custom snippets are read once by Go after their regular/readable
		// validation and carried in the in-memory snapshot. The renderer never
		// asks Nix to import a mutable user file at evaluation time.
		if site.Nginx.Handler.Type == "custom" {
			content, readErr := readRegularNoFollow(site.Nginx.Handler.Path, maxCustomSnippetBytes)
			if readErr != nil {
				return Snapshot{}, newStateError("invalid_handler", "cannot read custom handler", readErr)
			}
			if validateErr := nginxsnippet.Validate(string(content)); validateErr != nil {
				return Snapshot{}, newStateError("invalid_handler", "custom handler is not a permitted location snippet", validateErr)
			}
			site.Nginx.Handler.Content = string(content)
		}
		if e.Name() != site.ID+".yaml" {
			return Snapshot{}, newStateError("invalid_site_filename", "site manifest filename must equal its id", nil)
		}
		sites = append(sites, site)
	}
	sort.Slice(sites, func(i, j int) bool { return sites[i].ID < sites[j].ID })
	snap := Snapshot{Config: cfg, Sites: sites}
	return snap, snap.Validate()
}

// WriteSnapshot validates the complete candidate before publishing any file.
// Each individual publication is fsync+rename atomic. If a later publication
// fails, original state files are restored from private in-memory copies; this
// provides all-or-nothing snapshot semantics until Stage 5 adds a durable
// transaction journal around rebuilds.
func (s *Store) WriteSnapshot(snap Snapshot) error {
	return securefs.WithPrivateUmask(func() error { return s.writeSnapshot(snap) })
}

func (s *Store) writeSnapshot(snap Snapshot) (err error) {
	snap.Canonicalize()
	if err := snap.Validate(); err != nil {
		return err
	}
	for _, d := range []string{s.Root, s.SitesPath()} {
		if err := ensurePrivateDir(d); err != nil {
			return err
		}
	}
	if err := checkOwnedBy(s.Root, snap.Config.Owner.UID); err != nil {
		return err
	}
	before, hadBefore, err := s.captureStateFiles()
	if err != nil {
		return err
	}
	published := false
	defer func() {
		if err != nil && published {
			_ = s.restoreStateFiles(before, hadBefore)
		}
	}()
	files := map[string][]byte{}
	cfg, err := marshalCanonical(snap.Config)
	if err != nil {
		return err
	}
	files[s.ConfigPath()] = cfg
	for _, site := range snap.Sites {
		b, encodeErr := marshalCanonical(site)
		if encodeErr != nil {
			return encodeErr
		}
		files[filepath.Join(s.SitesPath(), site.ID+".yaml")] = b
	}
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if err = atomicWrite(path, files[path]); err != nil {
			return err
		}
		published = true
	}
	entries, err := os.ReadDir(s.SitesPath())
	if err != nil {
		return err
	}
	for _, e := range entries {
		path := filepath.Join(s.SitesPath(), e.Name())
		if _, keep := files[path]; keep {
			continue
		}
		if e.IsDir() || filepath.Ext(e.Name()) != ".yaml" || checkRegularPrivate(path) != nil {
			return newStateError("unsafe_state_path", "refusing to remove unmanaged site entry", nil)
		}
		if err = os.Remove(path); err != nil {
			return err
		}
		published = true
	}
	return syncDir(s.SitesPath())
}

func (s *Store) captureStateFiles() (map[string][]byte, map[string]bool, error) {
	result, exists := map[string][]byte{}, map[string]bool{}
	for _, dir := range []string{s.Root, s.SitesPath()} {
		entries, err := os.ReadDir(dir)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		for _, e := range entries {
			if dir == s.Root && e.Name() != "config.yaml" {
				continue
			}
			if dir == s.SitesPath() && filepath.Ext(e.Name()) != ".yaml" {
				continue
			}
			path := filepath.Join(dir, e.Name())
			if err := checkRegularPrivate(path); err != nil {
				return nil, nil, err
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return nil, nil, err
			}
			result[path], exists[path] = b, true
		}
	}
	return result, exists, nil
}
func (s *Store) restoreStateFiles(files map[string][]byte, exists map[string]bool) error {
	for path, data := range files {
		if err := atomicWrite(path, data); err != nil {
			return err
		}
	}
	entries, err := os.ReadDir(s.SitesPath())
	if err != nil {
		return err
	}
	for _, e := range entries {
		path := filepath.Join(s.SitesPath(), e.Name())
		if filepath.Ext(e.Name()) == ".yaml" && !exists[path] {
			if err := os.Remove(path); err != nil {
				return err
			}
		}
	}
	if !exists[s.ConfigPath()] {
		_ = os.Remove(s.ConfigPath())
	}
	return syncDir(s.SitesPath())
}

func (s *Store) Canonicalize() error {
	snap, err := s.Load()
	if err != nil {
		return err
	}
	return s.WriteSnapshot(snap)
}

func (s *Snapshot) Canonicalize() {
	s.Config.Canonicalize()
	for i := range s.Sites {
		s.Sites[i].Canonicalize()
	}
	sort.Slice(s.Sites, func(i, j int) bool { return s.Sites[i].ID < s.Sites[j].ID })
}
func ensurePrivateDir(path string) error {
	if err := os.MkdirAll(path, dirMode); err != nil {
		return err
	}
	return checkPrivateDir(path)
}
func checkPrivateDir(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&0077 != 0 {
		return newStateError("unsafe_state_path", "managed directory must be a private non-symlink directory", nil)
	}
	return nil
}
func checkRegularPrivate(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != fileMode {
		return newStateError("unsafe_state_path", "managed file must be regular and mode 0600", nil)
	}
	return nil
}
func readPrivateRegular(path string, uid, limit int) ([]byte, error) {
	b, err := readRegularNoFollow(path, int64(limit))
	if err != nil {
		return nil, err
	}
	if err := checkOwnedBy(path, uid); err != nil {
		return nil, err
	}
	return b, nil
}

// readRegularNoFollow validates the object actually opened, preventing a
// symlink or replacement between pathname validation and content parsing.
func readRegularNoFollow(path string, limit int64) ([]byte, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	var stat syscall.Stat_t
	if err := syscall.Fstat(fd, &stat); err != nil {
		return nil, err
	}
	if stat.Mode&syscall.S_IFMT != syscall.S_IFREG || stat.Size < 0 || stat.Size > limit {
		return nil, newStateError("unsafe_state_path", "managed file is not a regular file within the size limit", nil)
	}
	return io.ReadAll(io.LimitReader(f, limit+1))
}

func checkOwnedBy(path string, uid int) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	if int(stat.Uid) != uid {
		return newStateError("unsafe_state_path", "managed path has unexpected owner", nil)
	}
	return nil
}
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := ensurePrivateDir(dir); err != nil {
		return err
	}
	if _, err := os.Lstat(path); err == nil {
		if err := checkRegularPrivate(path); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".nixcp-")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(fileMode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	return syncDir(dir)
}
func syncDir(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// MarshalConfig serializes a validated config document into canonical YAML for transactions.
// MarshalSite serializes a validated site document for transactions.
func MarshalSite(site SiteConfig) ([]byte, error) {
	site.Canonicalize()
	if err := ValidateSite(site); err != nil {
		return nil, err
	}
	return marshalCanonical(site)
}

func MarshalConfig(cfg ConfigSnapshot) ([]byte, error) {
	cfg.Canonicalize()
	if err := ValidateConfig(cfg); err != nil {
		return nil, err
	}
	return marshalCanonical(cfg)
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
	out := b.Bytes()
	if !strings.HasSuffix(string(out), "\n") {
		out = append(out, '\n')
	}
	return out, nil
}
