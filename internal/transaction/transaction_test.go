package transaction

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type fakeRebuild struct {
	build, sw, rb, cur                  int
	failBuild, failSwitch, failRollback error
	order                               *[]string
}

func (f *fakeRebuild) CurrentGeneration(context.Context) (string, error) {
	f.cur++
	*f.order = append(*f.order, "current")
	return "generation-old", nil
}
func (f *fakeRebuild) Build(_ context.Context, p string) error {
	f.build++
	*f.order = append(*f.order, "build:"+filepath.Base(p))
	return f.failBuild
}
func (f *fakeRebuild) Switch(context.Context) error {
	f.sw++
	*f.order = append(*f.order, "switch")
	return f.failSwitch
}
func (f *fakeRebuild) Rollback(context.Context, string) error {
	f.rb++
	*f.order = append(*f.order, "rollback")
	return f.failRollback
}

type fakeHealth struct {
	err   error
	calls int
	order *[]string
}

func (h *fakeHealth) Check(context.Context, []string) error {
	h.calls++
	*h.order = append(*h.order, "health")
	return h.err
}
func manager(t *testing.T, r *fakeRebuild, h *fakeHealth) *Manager {
	t.Helper()
	root := t.TempDir()
	return &Manager{Root: root, Locker: FlockLocker{Path: filepath.Join(root, "lock")}, Rebuilder: r, Health: h, NewID: func() string { return "tx" }, Now: func() time.Time { return time.Unix(1, 0) }}
}
func TestApplyBuildsCandidateThenPublishesSwitchesAndChecksHealth(t *testing.T) {
	order := []string{}
	r := &fakeRebuild{order: &order}
	h := &fakeHealth{order: &order}
	m := manager(t, r, h)
	res, e := m.Apply(context.Background(), Request{Files: map[string][]byte{"generated/nixcp-module.nix": []byte("candidate"), "config.yaml": []byte("new")}, CandidateModule: "generated/nixcp-module.nix", Affected: []string{"nginx"}})
	if e != nil {
		t.Fatal(e)
	}
	if !res.Changed || res.Phase != PhaseCommitted {
		t.Fatalf("%+v", res)
	}
	if got := string(mustRead(t, filepath.Join(m.Root, "config.yaml"))); got != "new" {
		t.Fatal(got)
	}
	want := "build:nixcp-module.nix,current,switch,health"
	if got := join(order); got != want {
		t.Fatalf("order %s want %s", got, want)
	}
	j, e := m.readJournal(filepath.Join(m.Root, "transactions", "tx"))
	if e != nil || j.Phase != PhaseCommitted {
		t.Fatalf("journal=%+v err=%v", j, e)
	}
}
func TestFailureAfterPublishRestoresFilesAndGeneration(t *testing.T) {
	order := []string{}
	r := &fakeRebuild{order: &order, failSwitch: errors.New("switch failed")}
	h := &fakeHealth{order: &order}
	m := manager(t, r, h)
	mustWrite(t, filepath.Join(m.Root, "config.yaml"), []byte("old"))
	_, e := m.Apply(context.Background(), Request{Files: map[string][]byte{"config.yaml": []byte("new")}, CandidateModule: "config.yaml"})
	if e == nil {
		t.Fatal("expected error")
	}
	if got := string(mustRead(t, filepath.Join(m.Root, "config.yaml"))); got != "old" {
		t.Fatalf("not restored %q", got)
	}
	if r.rb != 1 {
		t.Fatalf("rollback %d", r.rb)
	}
	j, _ := m.readJournal(filepath.Join(m.Root, "transactions", "tx"))
	if j.Phase != PhaseRolledBack {
		t.Fatalf("%+v", j)
	}
}
func TestHealthFailureRollsBackAndNoopDoesNotRunPrivilegedSteps(t *testing.T) {
	order := []string{}
	r := &fakeRebuild{order: &order}
	h := &fakeHealth{order: &order, err: errors.New("bad health")}
	m := manager(t, r, h)
	mustWrite(t, filepath.Join(m.Root, "config.yaml"), []byte("old"))
	_, e := m.Apply(context.Background(), Request{Files: map[string][]byte{"config.yaml": []byte("new")}, CandidateModule: "config.yaml"})
	if e == nil || r.rb != 1 {
		t.Fatalf("err %v rb %d", e, r.rb)
	}
	h.err = nil
	res, e := m.Apply(context.Background(), Request{Files: map[string][]byte{"config.yaml": []byte("old")}, CandidateModule: "config.yaml"})
	if e != nil || res.Changed || r.build != 1 {
		t.Fatalf("res=%+v err=%v build=%d", res, e, r.build)
	}
}
func TestBuildFailureDoesNotPublish(t *testing.T) {
	order := []string{}
	r := &fakeRebuild{order: &order, failBuild: errors.New("no build")}
	h := &fakeHealth{order: &order}
	m := manager(t, r, h)
	mustWrite(t, filepath.Join(m.Root, "config.yaml"), []byte("old"))
	_, e := m.Apply(context.Background(), Request{Files: map[string][]byte{"config.yaml": []byte("new")}, CandidateModule: "config.yaml"})
	if e == nil || string(mustRead(t, filepath.Join(m.Root, "config.yaml"))) != "old" || r.sw != 0 {
		t.Fatalf("build failure published")
	}
}
func TestStalePublishedJournalRecovers(t *testing.T) {
	order := []string{}
	r := &fakeRebuild{order: &order}
	h := &fakeHealth{order: &order}
	m := manager(t, r, h)
	dir := filepath.Join(m.Root, "transactions", "stale")
	mustWrite(t, filepath.Join(m.Root, "config.yaml"), []byte("new"))
	if e := m.writeFiles(filepath.Join(dir, "backup"), map[string][]byte{"config.yaml": []byte("old")}); e != nil {
		t.Fatal(e)
	}
	if e := m.writeJournal(dir, &Journal{ID: "stale", Phase: PhasePublished, OldGeneration: "/nix/store/generation-old", CandidateHashes: hashes(map[string][]byte{"config.yaml": []byte("new")})}); e != nil {
		t.Fatal(e)
	}
	if e := m.Recover(context.Background()); e == nil {
		t.Fatal("recovery returns original stale error")
	}
	if got := string(mustRead(t, filepath.Join(m.Root, "config.yaml"))); got != "old" {
		t.Fatal(got)
	}
	j, _ := m.readJournal(dir)
	if j.Phase != PhaseRolledBack {
		t.Fatal(j.Phase)
	}
}

func TestStalePrePublishJournalWithoutGenerationIsClosedWithoutRollback(t *testing.T) {
	order := []string{}
	r := &fakeRebuild{order: &order}
	h := &fakeHealth{order: &order}
	m := manager(t, r, h)
	dir := filepath.Join(m.Root, "transactions", "pre-publish")
	if e := m.writeJournal(dir, &Journal{ID: "pre-publish", Phase: PhaseRollbackFailed}); e != nil {
		t.Fatal(e)
	}

	if e := m.Recover(context.Background()); e != nil {
		t.Fatalf("pre-publication recovery failed: %v", e)
	}
	j, e := m.readJournal(dir)
	if e != nil {
		t.Fatal(e)
	}
	if j.Phase != PhaseRolledBack {
		t.Fatalf("phase = %q, want %q", j.Phase, PhaseRolledBack)
	}
	if r.rb != 0 {
		t.Fatalf("pre-publication journal must not roll back a generation; calls=%d", r.rb)
	}
}
func TestExclusiveLockHonorsContext(t *testing.T) {
	root := t.TempDir()
	l := FlockLocker{Path: filepath.Join(root, "lock"), Retry: time.Millisecond}
	one, e := l.Acquire(context.Background(), Exclusive)
	if e != nil {
		t.Fatal(e)
	}
	defer one.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, e := l.Acquire(ctx, Exclusive); e == nil {
		t.Fatal("lock unexpectedly acquired")
	}
}
func TestConcurrentMutationsSerialize(t *testing.T) {
	order := []string{}
	r := &fakeRebuild{order: &order}
	h := &fakeHealth{order: &order}
	m := manager(t, r, h)
	var wg sync.WaitGroup
	for _, v := range []string{"one", "two"} {
		wg.Add(1)
		go func(v string) {
			defer wg.Done()
			_, _ = m.Apply(context.Background(), Request{Files: map[string][]byte{"config.yaml": []byte(v)}, CandidateModule: "config.yaml"})
		}(v)
	}
	wg.Wait()
	if r.build != 2 {
		t.Fatalf("builds %d", r.build)
	}
}
func mustWrite(t *testing.T, p string, b []byte) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, b, 0600); e != nil {
		t.Fatal(e)
	}
}
func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func join(a []string) string {
	out := ""
	for i, v := range a {
		if i > 0 {
			out += ","
		}
		out += v
	}
	return out
}
