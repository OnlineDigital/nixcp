package site

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCanonicalizePathResolvesSymlinkChain(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.Mkdir(real, 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	got, err := CanonicalizePath(link)
	if err != nil {
		t.Fatal(err)
	}
	if got != real {
		t.Fatalf("expected %q, got %q", real, got)
	}
}

func TestCanonicalizePathRejectsEmptyAndMissing(t *testing.T) {
	if _, err := CanonicalizePath("   "); err == nil {
		t.Fatal("empty path must be rejected")
	}
	if _, err := CanonicalizePath(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("missing path must be rejected")
	}
}

func TestCanonicalizePathRejectsSymlinkLoop(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	if err := os.Symlink(b, a); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(a, b); err != nil {
		t.Fatal(err)
	}
	if _, err := CanonicalizePath(a); err == nil {
		t.Fatal("symlink loop must be rejected within MaxSymlinkHops")
	}
}

func TestRefuseWorldWritableAllowsStickyTmp(t *testing.T) {
	// t.TempDir() lives under a sticky /tmp on CI hosts; linking must work.
	if err := RefuseWorldWritable(t.TempDir()); err != nil {
		t.Fatalf("sticky tmp dir must be allowed: %v", err)
	}
}

func TestRefuseWorldWritableRejectsPlainWorldWritableAncestor(t *testing.T) {
	base := t.TempDir()
	world := filepath.Join(base, "world")
	if err := os.Mkdir(world, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(world, 0777); err != nil {
		t.Fatal(err)
	}
	// strip sticky if the fs imposed it
	info, err := os.Lstat(world)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSticky != 0 {
		if err := os.Chmod(world, info.Mode()&^os.ModeSticky); err != nil {
			t.Fatal(err)
		}
	}
	proj := filepath.Join(world, "proj")
	if err := os.Mkdir(proj, 0755); err != nil {
		t.Fatal(err)
	}
	if err := RefuseWorldWritable(proj); err == nil {
		t.Fatal("world-writable non-sticky ancestor must be refused")
	}
}

func TestDescribeDisabledSiteNotHealthy(t *testing.T) {
	// A disabled site must never be described as healthy, even when probes
	// happen to succeed or the problem code is unset.
	cases := []HealthStatus{
		{Domain: "off.example", DesiredOn: false, SocketOK: false, HTTPOK: false},
		{Domain: "off2.example", DesiredOn: false, SocketOK: true, HTTPOK: true, HTTPStatus: 200},
		{Domain: "on.example", DesiredOn: true, SocketOK: false, HTTPOK: false},
		{Domain: "half.example", DesiredOn: true, SocketOK: true, HTTPOK: false},
	}
	for _, c := range cases {
		got := c.Describe()
		if strings.Contains(got, "healthy") {
			t.Errorf("status %#v described as healthy: %q", c, got)
		}
	}
	// The only "healthy" case: enabled and both probes passing.
	ok := HealthStatus{Domain: "ok.example", DesiredOn: true, SocketOK: true, HTTPOK: true, HTTPStatus: 200}
	if s := ok.Describe(); !strings.Contains(s, "healthy") {
		t.Errorf("healthy site misdescribed: %q", s)
	}
}
