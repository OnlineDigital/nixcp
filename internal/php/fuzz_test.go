package php

import "testing"

func FuzzParseMarker(f *testing.F) {
	for _, seed := range []string{"8.3", "8.4\n", " 8.3", "8.3 8.4", "8.2", "${bad}"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, marker string) {
		version, err := parseMarker(marker)
		if err == nil && version != "8.3" && version != "8.4" {
			t.Fatalf("accepted unsupported marker %q", version)
		}
	})
}
