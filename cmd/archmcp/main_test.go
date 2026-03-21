package main

import (
	"bytes"
	"strings"
	"testing"
)

// Acceptance test: --version prints version and exits
func TestAcceptance_VersionFlag(t *testing.T) {
	// Test --version
	var stdout, stderr bytes.Buffer
	code := run([]string{"--version"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("--version exit code = %d, want 0", code)
	}
	out := stdout.String()
	if !strings.Contains(out, "archmcp") {
		t.Errorf("--version output missing 'archmcp': %q", out)
	}
	if !strings.Contains(out, "dev") {
		t.Errorf("--version output missing version string: %q", out)
	}

	// Test -v (short form)
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"-v"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("-v exit code = %d, want 0", code)
	}
	if stdout.String() != out {
		t.Errorf("-v output = %q, want same as --version: %q", stdout.String(), out)
	}
}

// Acceptance test: --help prints usage and exits
func TestAcceptance_HelpFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--help"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("--help exit code = %d, want 0", code)
	}
	out := stdout.String()
	if !strings.Contains(out, "archmcp") {
		t.Errorf("--help output missing 'archmcp': %q", out)
	}
	if !strings.Contains(out, "Usage") {
		t.Errorf("--help output missing 'Usage': %q", out)
	}
	// Should mention --generate flag
	if !strings.Contains(out, "--generate") {
		t.Errorf("--help output missing '--generate': %q", out)
	}
	// Should mention config file
	if !strings.Contains(out, "config") {
		t.Errorf("--help output missing 'config': %q", out)
	}

	// Test -h (short form)
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"-h"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("-h exit code = %d, want 0", code)
	}
	if stdout.String() != out {
		t.Errorf("-h output = %q, want same as --help: %q", stdout.String(), out)
	}
}

// Acceptance test: Version variable can be overridden (simulates ldflags)
func TestAcceptance_VersionOverride(t *testing.T) {
	old := Version
	defer func() { Version = old }()

	Version = "v1.2.3"
	var stdout, stderr bytes.Buffer
	code := run([]string{"--version"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "v1.2.3") {
		t.Errorf("output missing overridden version: %q", stdout.String())
	}
}
