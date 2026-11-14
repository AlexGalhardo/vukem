package main

import "testing"

func TestIsPrerelease(t *testing.T) {
	tests := []struct {
		version string
		want    bool
	}{
		{"v0.43.0", false},
		{"v1.0.0", false},
		{"v1.0.0-rc.1", true},
		{"v0.44.0-beta.2", true},
	}
	for _, tt := range tests {
		if got := isPrerelease(tt.version); got != tt.want {
			t.Errorf("isPrerelease(%q) = %v, want %v", tt.version, got, tt.want)
		}
	}
}
