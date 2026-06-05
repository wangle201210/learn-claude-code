package main

import "testing"

func TestIsManualCompact(t *testing.T) {
	cases := map[string]bool{
		"/compact":     true,
		"compact":      true,
		" COMPACT ":    true,
		"/Compact":     true,
		"":             false,
		"compact now":  false,
		"/compact now": false,
	}

	for input, want := range cases {
		if got := isManualCompact(input); got != want {
			t.Fatalf("isManualCompact(%q) = %v, want %v", input, got, want)
		}
	}
}
