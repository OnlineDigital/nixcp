package php

import "testing"

func FuzzParseMarker(f *testing.F) {
	for _, seed := range []string{"8.2", "8.3", "8.4\n", "8.5", " 8.3", "8.3 8.4", "8.1", "${bad}"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, marker string) {
		version, err := parseMarker(marker)
		if err == nil && version != "8.2" && version != "8.3" && version != "8.4" && version != "8.5" {
			t.Fatalf("accepted unsupported marker %q", version)
		}
	})
}
