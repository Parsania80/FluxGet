package main

import "testing"

func TestProgressValue(t *testing.T) {
	for _, tc := range []struct {
		message string
		value   float64
		known   bool
	}{
		{"Downloaded 25/100 bytes (25.0%), 100 bytes/s", 0.25, true},
		{"Job 3: Downloaded 3/4 bytes (75.0%)", 0.75, true},
		{"Downloaded 100/100 bytes (100.0%)", 1, true},
		{"Downloaded 100 bytes, 50 bytes/s", 0, false},
		{"Downloaded 0/0 bytes", 0, false},
		{"Downloaded 11/10 bytes", 0, false},
		{"Connecting…", 0, false},
	} {
		value, known := progressValue(tc.message)
		if value != tc.value || known != tc.known {
			t.Errorf("progressValue(%q) = (%v, %v), want (%v, %v)", tc.message, value, known, tc.value, tc.known)
		}
	}
}
