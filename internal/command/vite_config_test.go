package command

import (
	"strings"
	"testing"
)

func TestViteEditorName(t *testing.T) {
	t.Setenv("EDITOR", "code --wait")
	if got := viteEditorName(); got != "code" {
		t.Fatalf("editor name = %q, want code", got)
	}
	t.Setenv("EDITOR", "")
	if got := viteEditorName(); got != "" {
		t.Fatalf("editor name = %q, want empty", got)
	}
}

func TestPatchViteConfigKeepsExistingTrailingComma(t *testing.T) {
	in := "export default {\n  plugins: [],\n}\n"
	out, err := patchViteConfig([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	if strings.Contains(text, "plugins: [],\n,\n") {
		t.Fatalf("inserted a standalone comma:\n%s", text)
	}
	if !strings.Contains(text, "},\n}") {
		t.Fatalf("new server object must have a trailing comma:\n%s", text)
	}
	if !strings.Contains(text, "    server: {\n        host:") {
		t.Fatalf("new server children must be indented one level further:\n%s", text)
	}
}

func TestPatchViteConfigAddsServerWithoutReserializing(t *testing.T) {
	in := "import { defineConfig } from 'vite'\n\nexport default defineConfig({\n  // Preserve me\n  plugins: [],\n})\n"
	out, err := patchViteConfig([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	for _, want := range []string{"// Preserve me", "plugins: []", "server:", "host: '0.0.0.0'", "host: 'renew-crm.p.ohost.cloud'", "protocol: 'wss'", "clientPort: 443", "'**/storage/framework/views/**'"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q:\n%s", want, text)
		}
	}
}

func TestPatchViteConfigMergesExistingServer(t *testing.T) {
	in := "export default {\n  server: {\n    // retain\n    port: 5173,\n    hmr: { overlay: false },\n  },\n}\n"
	out, err := patchViteConfig([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	for _, want := range []string{"// retain", "port: 5173", "overlay: false", "host: '0.0.0.0'", "protocol: 'wss'", "clientPort: 443", "watch:"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q:\n%s", want, text)
		}
	}
}

func TestPatchViteConfigIsIdempotent(t *testing.T) {
	in := []byte("export default {}\n")
	one, err := patchViteConfig(in)
	if err != nil {
		t.Fatal(err)
	}
	two, err := patchViteConfig(one)
	if err != nil {
		t.Fatal(err)
	}
	if string(one) != string(two) {
		t.Fatalf("not idempotent:\n%s\n---\n%s", one, two)
	}
}
