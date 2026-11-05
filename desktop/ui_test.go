package main

import "testing"

func TestJobVisible(t *testing.T) {
	for _, tc := range []struct {
		category, state string
		want            bool
	}{
		{"All downloads", "Completed", true},
		{"All downloads", "Paused", true},
		{"Finished", "Completed", true},
		{"Finished", "Paused", false},
		{"Unfinished", "Failed", true},
		{"Unfinished", "Completed", false},
		{"Unknown", "Completed", false},
	} {
		if got := jobVisible(tc.category, tc.state); got != tc.want {
			t.Errorf("jobVisible(%q, %q) = %t, want %t", tc.category, tc.state, got, tc.want)
		}
	}
}
