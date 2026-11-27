package cloudserver

import (
	"slices"
	"testing"
)

func TestDurabilityFor(t *testing.T) {
	keys := func(version string) []string {
		var out []string
		for _, kv := range durabilityFor(version) {
			out = append(out, kv[0])
		}
		return out
	}
	newer := []string{"core.fsync", "core.fsyncMethod"}
	older := []string{"core.fsyncObjectFiles"}
	for version, want := range map[string][]string{
		"git version 2.34.1\n":         older,
		"git version 2.35.8":           older,
		"git version 2.36.0":           newer,
		"git version 2.39.5":           newer,
		"git version 3.0.0":            newer,
		"git version 2.40.0.windows.1": newer,
		"something else":               append(slices.Clone(newer), older...),
	} {
		if got := keys(version); !slices.Equal(got, want) {
			t.Errorf("%q: %v, want %v", version, got, want)
		}
	}
}
