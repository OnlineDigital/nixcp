package state

import (
	"os"
	"path/filepath"
	"testing"
)

func validConfig(home string) ConfigSnapshot {
	return ConfigSnapshot{SchemaVersion: 1, Owner: Owner{Username: "u", UID: os.Getuid(), Group: "g", GID: os.Getgid(), Home: home}, Platform: Platform{System: "x86_64-linux"}, Rebuild: RebuildConfig{Mode: "traditional"}, Services: ServiceStates{Nginx: ServiceConfig{DesiredState: "stopped"}, MariaDB: ServiceConfig{DesiredState: "stopped"}, Redis: ServiceConfig{DesiredState: "stopped"}}}
}
func TestStoreRoundTripAndRejectsUnsafeFile(t *testing.T) {
	home := t.TempDir()
	s := NewStore(home)
	if err := s.Initialize(validConfig(home)); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil || got.Config.Owner.Home != home {
		t.Fatalf("load = %#v, %v", got, err)
	}
	if err := os.Chmod(s.ConfigPath(), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(); err == nil {
		t.Fatal("expected insecure mode rejection")
	}
}
func TestStrictYAMLRejectsUnknownAndDuplicateKeys(t *testing.T) {
	cfg := validConfig("/tmp/u")
	b, err := marshalCanonical(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NormalizeAndValidateConfig(append(b, []byte("unknown: x\n")...)); err == nil {
		t.Fatal("expected unknown key error")
	}
	raw := []byte("schemaVersion: 1\nschemaVersion: 1\n")
	if _, err := NormalizeAndValidateConfig(raw); err == nil {
		t.Fatal("expected duplicate-key error")
	}
}
func TestSnapshotCrossValidation(t *testing.T) {
	cfg := validConfig("/tmp/u")
	site := SiteConfig{SchemaVersion: 1, ID: "example-com", Enabled: true, Domain: "example.com", ProjectPath: "/srv/p", DocumentRoot: "/srv/p/public", PHP: "8.3", Nginx: NginxConfig{Handler: HandlerConfig{Type: "generic"}}}
	if err := (Snapshot{Config: cfg, Sites: []SiteConfig{site}}).Validate(); err == nil {
		t.Fatal("expected nginx/php prerequisite error")
	}
	_ = filepath.Separator
}
