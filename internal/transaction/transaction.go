// Package transaction implements the durable, locked publication protocol used
// by NixCP persistent mutations. It deliberately has no Cobra dependencies.
package transaction

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/nixcp/nixcp/internal/securefs"
	"gopkg.in/yaml.v3"
)

const (
	PhaseCreated        = "created"
	PhaseStaged         = "staged"
	PhaseBuilt          = "built"
	PhasePublished      = "published"
	PhaseSwitched       = "switched"
	PhaseVerified       = "verified"
	PhaseCommitted      = "committed"
	PhaseRollingBack    = "rolling-back"
	PhaseRolledBack     = "rolled-back"
	PhaseRollbackFailed = "rollback-failed"
)

// Journal is persisted after every externally observable phase. Hashes make a
// partially published transaction diagnosable without trusting file names.
type Journal struct {
	ID                string            `yaml:"id"`
	Phase             string            `yaml:"phase"`
	OldGeneration     string            `yaml:"oldGeneration,omitempty"`
	OldHashes         map[string]string `yaml:"oldHashes"`
	CandidateHashes   map[string]string `yaml:"candidateHashes"`
	AffectedResources []string          `yaml:"affectedResources,omitempty"`
	StartedAt         time.Time         `yaml:"startedAt"`
	Error             string            `yaml:"error,omitempty"`
	RollbackError     string            `yaml:"rollbackError,omitempty"`
}

type LockMode int

const (
	Shared LockMode = iota
	Exclusive
)

type Lock interface{ Unlock() error }
type Locker interface {
	Acquire(context.Context, LockMode) (Lock, error)
}

// FlockLocker is an advisory, kernel released lock. It never relies on a PID
// file for correctness; the journal handles process crashes after publication.
type FlockLocker struct {
	Path  string
	Retry time.Duration
}

var processLocks sync.Map // map[path]*sync.RWMutex; flock is reentrant within a process.
func localLock(path string) *sync.RWMutex {
	value, _ := processLocks.LoadOrStore(path, &sync.RWMutex{})
	return value.(*sync.RWMutex)
}

func (l FlockLocker) Acquire(ctx context.Context, mode LockMode) (Lock, error) {
	if l.Retry <= 0 {
		l.Retry = 25 * time.Millisecond
	}
	if err := os.MkdirAll(filepath.Dir(l.Path), 0700); err != nil {
		return nil, err
	}
	local := localLock(l.Path)
	for {
		acquired := false
		if mode == Exclusive {
			acquired = local.TryLock()
		} else {
			acquired = local.TryRLock()
		}
		if acquired {
			break
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("lock timeout/cancelled: %w", ctx.Err())
		case <-time.After(l.Retry):
		}
	}
	f, err := os.OpenFile(l.Path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		if mode == Exclusive {
			local.Unlock()
		} else {
			local.RUnlock()
		}
		return nil, err
	}
	how := syscall.LOCK_SH
	if mode == Exclusive {
		how = syscall.LOCK_EX
	}
	for {
		if err := syscall.Flock(int(f.Fd()), how|syscall.LOCK_NB); err == nil {
			return &flock{f: f, local: local, exclusive: mode == Exclusive}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			_ = f.Close()
			if mode == Exclusive {
				local.Unlock()
			} else {
				local.RUnlock()
			}
			return nil, err
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			if mode == Exclusive {
				local.Unlock()
			} else {
				local.RUnlock()
			}
			return nil, fmt.Errorf("lock timeout/cancelled: %w", ctx.Err())
		case <-time.After(l.Retry):
		}
	}
}

type flock struct {
	f         *os.File
	local     *sync.RWMutex
	exclusive bool
	once      sync.Once
	err       error
}

func (l *flock) Unlock() error {
	l.once.Do(func() {
		l.err = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
		if e := l.f.Close(); l.err == nil {
			l.err = e
		}
		if l.exclusive {
			l.local.Unlock()
		} else {
			l.local.RUnlock()
		}
	})
	return l.err
}

// Rebuilder is intentionally narrow: the caller gives no user-controlled argv.
type Rebuilder interface {
	CurrentGeneration(context.Context) (string, error)
	Build(context.Context, string) error
	Switch(context.Context) error
	Rollback(context.Context, string) error
}
type HealthChecker interface {
	Check(context.Context, []string) error
}

// CompositeHealth runs all configured non-mutating verification adapters.
// Each adapter decides whether the affected resource set concerns it.
type CompositeHealth []HealthChecker

func (h CompositeHealth) Check(ctx context.Context, affected []string) error {
	for _, checker := range h {
		if checker == nil {
			continue
		}
		if err := checker.Check(ctx, affected); err != nil {
			return err
		}
	}
	return nil
}

// Manager publishes a map of private files relative to Root. A missing old file
// is represented in the backup manifest and is removed during restoration.
type Manager struct {
	Root             string
	Locker           Locker
	Rebuilder        Rebuilder
	Health           HealthChecker
	CandidateWrapper *CandidateWrapper
	NewID            func() string
	Now              func() time.Time
}

// CandidateWrapper makes a staged NixCP module evaluable as part of the real
// traditional NixOS configuration. The stable module is disabled by its exact
// path and the staged replacement is imported instead. It is deliberately
// absent for callers that do not build NixOS modules.
type CandidateWrapper struct {
	ExistingConfig string
	StableModule   string
}

type Request struct {
	Files           map[string][]byte
	Deletes         []string // managed paths removed only after a successful candidate build
	CandidateModule string
	Affected        []string
}

type Result struct {
	ID      string
	Changed bool
	Phase   string
}

func (m *Manager) Apply(ctx context.Context, r Request) (Result, error) {
	if len(r.Files) == 0 && len(r.Deletes) == 0 {
		return Result{Changed: false, Phase: PhaseCommitted}, nil
	}
	if err := m.valid(); err != nil {
		return Result{}, err
	}
	lock, err := m.Locker.Acquire(ctx, Exclusive)
	if err != nil {
		return Result{}, err
	}
	defer lock.Unlock()
	if err = m.Recover(ctx); err != nil {
		return Result{}, err
	}
	if err = validateFiles(r.Files); err != nil {
		return Result{}, err
	}
	if err = validateDeletes(r.Deletes); err != nil {
		return Result{}, err
	}
	managed := managedFiles(r.Files, r.Deletes)
	old, err := m.capture(managed)
	if err != nil {
		return Result{}, err
	}
	if equalFiles(old, r.Files) && deletesAbsent(old, r.Deletes) {
		return Result{Changed: false, Phase: PhaseCommitted}, nil
	}
	id := m.id()
	dir := filepath.Join(m.Root, "transactions", id)
	if err = os.MkdirAll(filepath.Join(dir, "stage"), 0700); err != nil {
		return Result{}, err
	}
	candidateHashes := hashes(r.Files)
	for _, p := range r.Deletes {
		candidateHashes[p] = ""
	}
	j := Journal{ID: id, Phase: PhaseCreated, OldHashes: hashes(old), CandidateHashes: candidateHashes, AffectedResources: append([]string(nil), r.Affected...), StartedAt: m.now().UTC()}
	if err = m.writeJournal(dir, &j); err != nil {
		return Result{}, err
	}
	if err = m.writeFiles(filepath.Join(dir, "backup"), old); err != nil {
		return Result{}, m.failBeforePublish(dir, &j, err)
	}
	if err = m.writeFiles(filepath.Join(dir, "stage"), r.Files); err != nil {
		return Result{}, m.failBeforePublish(dir, &j, err)
	}
	j.Phase = PhaseStaged
	if err = m.writeJournal(dir, &j); err != nil {
		return Result{}, err
	}
	if !safeRelative(r.CandidateModule) {
		return Result{}, m.failBeforePublish(dir, &j, fmt.Errorf("candidate module must be a relative managed path"))
	}
	candidate := filepath.Join(dir, "stage", filepath.Clean(r.CandidateModule))
	if m.CandidateWrapper != nil {
		candidate, err = m.writeCandidateWrapper(dir, candidate)
		if err != nil {
			return Result{}, m.failBeforePublish(dir, &j, err)
		}
	}
	if err = m.Rebuilder.Build(ctx, candidate); err != nil {
		return Result{}, m.failBeforePublish(dir, &j, err)
	}
	j.Phase = PhaseBuilt
	if err = m.writeJournal(dir, &j); err != nil {
		return Result{}, err
	}
	generation, err := m.Rebuilder.CurrentGeneration(ctx)
	if err != nil {
		return Result{}, m.failBeforePublish(dir, &j, err)
	}
	j.OldGeneration = generation
	if err = m.writeJournal(dir, &j); err != nil {
		return Result{}, err
	}
	if err = m.publish(r.Files, r.Deletes); err != nil {
		return Result{}, m.rollback(ctx, dir, &j, old, managed, err)
	}
	j.Phase = PhasePublished
	if err = m.writeJournal(dir, &j); err != nil {
		return Result{}, m.rollback(ctx, dir, &j, old, managed, err)
	}
	if err = m.Rebuilder.Switch(ctx); err != nil {
		return Result{}, m.rollback(ctx, dir, &j, old, managed, err)
	}
	j.Phase = PhaseSwitched
	if err = m.writeJournal(dir, &j); err != nil {
		return Result{}, m.rollback(ctx, dir, &j, old, managed, err)
	}
	if err = m.Health.Check(ctx, r.Affected); err != nil {
		return Result{}, m.rollback(ctx, dir, &j, old, managed, err)
	}
	j.Phase = PhaseVerified
	if err = m.writeJournal(dir, &j); err != nil {
		return Result{}, m.rollback(ctx, dir, &j, old, managed, err)
	}
	j.Phase = PhaseCommitted
	if err = m.writeJournal(dir, &j); err != nil {
		return Result{}, err
	}
	_ = os.RemoveAll(filepath.Join(dir, "stage"))
	return Result{ID: id, Changed: true, Phase: j.Phase}, nil
}

func (m *Manager) writeCandidateWrapper(dir, stagedModule string) (string, error) {
	w := m.CandidateWrapper
	if w == nil {
		return stagedModule, nil
	}
	if !safeAbsolutePath(w.ExistingConfig) || !safeAbsolutePath(w.StableModule) || !safeAbsolutePath(stagedModule) {
		return "", fmt.Errorf("candidate wrapper paths must be safe absolute paths")
	}
	path := filepath.Join(dir, "stage", "candidate-wrapper.nix")
	contents := fmt.Sprintf(`{ ... }: {
  disabledModules = [ %s ];
  imports = [
    (builtins.toPath %s)
    (builtins.toPath %s)
  ];
}
`, nixString(w.StableModule), nixString(w.ExistingConfig), nixString(stagedModule))
	if err := writeAtomic(path, []byte(contents)); err != nil {
		return "", err
	}
	return path, nil
}

// Recover must be called while holding the exclusive lock. It restores every
// nonterminal journal deterministically before a later mutation may start.
func (m *Manager) Recover(ctx context.Context) error {
	entries, err := os.ReadDir(filepath.Join(m.Root, "transactions"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(m.Root, "transactions", e.Name())
		j, err := m.readJournal(dir)
		if err != nil {
			return err
		}
		if j.Phase == PhaseCommitted || j.Phase == PhaseRolledBack {
			continue
		}
		// OldGeneration is recorded immediately before publication. A crash
		// before that point cannot have changed managed files or switched the
		// host, so attempting a generation rollback is both unnecessary and
		// unsafe (the empty value is rightly rejected by the NixOS adapter).
		// This also repairs journals left as rollback-failed by older builds.
		if j.OldGeneration == "" {
			switch j.Phase {
			case PhaseCreated, PhaseStaged, PhaseBuilt, PhaseRollingBack, PhaseRollbackFailed:
				j.Phase = PhaseRolledBack
				j.RollbackError = ""
				if err := m.writeJournal(dir, &j); err != nil {
					return fmt.Errorf("recovery %s: %w", j.ID, err)
				}
				continue
			default:
				return fmt.Errorf("recovery %s: missing prior system generation at phase %s", j.ID, j.Phase)
			}
		}
		old, err := m.readFiles(filepath.Join(dir, "backup"))
		if err != nil {
			return fmt.Errorf("recovery %s: %w", j.ID, err)
		}
		if err = m.rollback(ctx, dir, &j, old, filesFromHashes(j.CandidateHashes), fmt.Errorf("recovering stale transaction")); err != nil {
			return err
		}
	}
	return nil
}
func (m *Manager) rollback(ctx context.Context, dir string, j *Journal, old, candidate map[string][]byte, cause error) error {
	j.Phase = PhaseRollingBack
	j.Error = cause.Error()
	_ = m.writeJournal(dir, j)
	stateErr := m.restore(old, candidate)
	genErr := m.Rebuilder.Rollback(ctx, j.OldGeneration)
	if stateErr != nil || genErr != nil {
		j.Phase = PhaseRollbackFailed
		j.RollbackError = joinErr(stateErr, genErr)
		_ = m.writeJournal(dir, j)
		return fmt.Errorf("%w; rollback failed: %s", cause, j.RollbackError)
	}
	j.Phase = PhaseRolledBack
	_ = m.writeJournal(dir, j)
	return cause
}
func (m *Manager) failBeforePublish(dir string, j *Journal, cause error) error {
	j.Error = cause.Error()
	_ = m.writeJournal(dir, j)
	return cause
}
func (m *Manager) valid() error {
	if m.Root == "" || m.Locker == nil || m.Rebuilder == nil || m.Health == nil {
		return fmt.Errorf("transaction manager is incomplete")
	}
	return os.MkdirAll(filepath.Join(m.Root, "transactions"), 0700)
}
func (m *Manager) id() string {
	if m.NewID != nil {
		return m.NewID()
	}
	return fmt.Sprintf("%d", m.now().UnixNano())
}
func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}
func safeRelative(p string) bool {
	return p != "" && !filepath.IsAbs(p) && filepath.Clean(p) == p && !strings.HasPrefix(p, ".."+string(filepath.Separator)) && p != ".."
}

func safeAbsolutePath(p string) bool {
	return p != "" && filepath.IsAbs(p) && filepath.Clean(p) == p && utf8.ValidString(p) && !strings.ContainsAny(p, "\x00\r\n")
}

// nixString encodes the small, fixed set of path strings used by the wrapper.
// In particular, `${` must not become interpolation when a home directory or
// transaction path happens to contain it.
func nixString(value string) string {
	replacer := strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		"\n", `\n`,
		"\r", `\r`,
		"\t", `\t`,
		"${", `\${`,
	)
	return `"` + replacer.Replace(value) + `"`
}
func validateDeletes(deletes []string) error {
	seen := map[string]struct{}{}
	for _, p := range deletes {
		allowed := (strings.HasPrefix(p, "sites/") && filepath.Ext(p) == ".yaml") || (strings.HasPrefix(p, "secrets/") && filepath.Ext(p) == ".sql")
		if !safeRelative(p) || p == "config.yaml" || p == "generated/nixcp-module.nix" || !allowed {
			return fmt.Errorf("unsafe managed delete path %q", p)
		}
		if _, ok := seen[p]; ok {
			return fmt.Errorf("duplicate managed delete path %q", p)
		}
		seen[p] = struct{}{}
	}
	return nil
}
func managedFiles(files map[string][]byte, deletes []string) map[string][]byte {
	out := make(map[string][]byte, len(files)+len(deletes))
	for p, b := range files {
		out[p] = b
	}
	for _, p := range deletes {
		if _, ok := out[p]; !ok {
			out[p] = nil
		}
	}
	return out
}
func deletesAbsent(old map[string][]byte, deletes []string) bool {
	for _, p := range deletes {
		if _, ok := old[p]; ok {
			return false
		}
	}
	return true
}
func validateFiles(files map[string][]byte) error {
	for p := range files {
		if !safeRelative(p) {
			return fmt.Errorf("unsafe managed path %q", p)
		}
	}
	return nil
}
func (m *Manager) capture(want map[string][]byte) (map[string][]byte, error) {
	out := map[string][]byte{}
	for p := range want {
		b, e := os.ReadFile(filepath.Join(m.Root, p))
		if e == nil {
			out[p] = b
		} else if !errors.Is(e, os.ErrNotExist) {
			return nil, e
		}
	}
	return out, nil
}
func equalFiles(old, next map[string][]byte) bool {
	if len(old) != len(next) {
		return false
	}
	for p, b := range next {
		if string(old[p]) != string(b) {
			return false
		}
	}
	return true
}
func hashes(files map[string][]byte) map[string]string {
	o := map[string]string{}
	for p, b := range files {
		h := sha256.Sum256(b)
		o[p] = hex.EncodeToString(h[:])
	}
	return o
}
func (m *Manager) writeFiles(base string, files map[string][]byte) error {
	for p, b := range files {
		path := filepath.Join(base, p)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		if err := writeAtomic(path, b); err != nil {
			return err
		}
	}
	return nil
}
func (m *Manager) readFiles(base string) (map[string][]byte, error) {
	out := map[string][]byte{}
	err := filepath.WalkDir(base, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		rel, e := filepath.Rel(base, p)
		if e != nil || !safeRelative(rel) {
			return fmt.Errorf("unsafe backup path")
		}
		b, e := os.ReadFile(p)
		if e == nil {
			out[rel] = b
		}
		return e
	})
	return out, err
}
func (m *Manager) publish(files map[string][]byte, deletes []string) error {
	for p, b := range files {
		if err := writeAtomic(filepath.Join(m.Root, p), b); err != nil {
			return err
		}
	}
	for _, p := range deletes {
		if err := os.Remove(filepath.Join(m.Root, p)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return syncDir(m.Root)
}
func (m *Manager) restore(old, candidate map[string][]byte) error {
	for p := range candidate {
		if _, existed := old[p]; !existed {
			if err := os.Remove(filepath.Join(m.Root, p)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	return m.publish(old, nil)
}
func filesFromHashes(h map[string]string) map[string][]byte {
	out := map[string][]byte{}
	for p := range h {
		out[p] = nil
	}
	return out
}
func writeAtomic(path string, b []byte) error {
	return securefs.WithPrivateUmask(func() error { return writeAtomicPrivate(path, b) })
}

func writeAtomicPrivate(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".ncp-")
	if e != nil {
		return e
	}
	n := f.Name()
	defer os.Remove(n)
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	if closeErr := f.Close(); e == nil {
		e = closeErr
	}
	if e == nil {
		e = os.Rename(n, path)
	}
	if e == nil {
		e = syncDir(filepath.Dir(path))
	}
	return e
}
func syncDir(path string) error {
	d, e := os.Open(path)
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}
func (m *Manager) writeJournal(dir string, j *Journal) error {
	b, e := yaml.Marshal(j)
	if e != nil {
		return e
	}
	return writeAtomic(filepath.Join(dir, "journal.yaml"), b)
}
func (m *Manager) readJournal(dir string) (Journal, error) {
	b, e := os.ReadFile(filepath.Join(dir, "journal.yaml"))
	if e != nil {
		return Journal{}, e
	}
	var j Journal
	e = yaml.Unmarshal(b, &j)
	if e == nil && (j.ID == "" || j.Phase == "") {
		e = fmt.Errorf("invalid journal")
	}
	return j, e
}
func joinErr(es ...error) string {
	a := []string{}
	for _, e := range es {
		if e != nil {
			a = append(a, e.Error())
		}
	}
	sort.Strings(a)
	return strings.Join(a, "; ")
}
