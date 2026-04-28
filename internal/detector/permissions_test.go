package detector

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCurrentUsernamePriority(t *testing.T) {
	t.Setenv("USER", "userA")
	t.Setenv("USERNAME", "userB")
	t.Setenv("LOGNAME", "userC")

	if got := currentUsername(); got != "userA" {
		t.Fatalf("expected userA, got %q", got)
	}

	t.Setenv("USER", "")
	if got := currentUsername(); got != "userB" {
		t.Fatalf("expected userB, got %q", got)
	}

	t.Setenv("USERNAME", "")
	if got := currentUsername(); got != "userC" {
		t.Fatalf("expected userC, got %q", got)
	}
}

func TestIsWritable(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "a.txt")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if !isWritable(f) {
		t.Fatalf("expected file to be writable")
	}
	if isWritable(filepath.Join(tmp, "missing")) {
		t.Fatalf("expected missing file to be non-writable")
	}
}

func TestFindRunnerBinaryFromPath(t *testing.T) {
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "gitlab-runner")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write bin: %v", err)
	}
	t.Setenv("PATH", tmp)

	got := findRunnerBinary("linux")
	if got == "" {
		t.Fatalf("expected runner binary path")
	}
}

func TestIsWindowsAdminNonWindows(t *testing.T) {
	if runtime.GOOS != "windows" && isWindowsAdmin() {
		t.Fatalf("expected false on non-windows")
	}
}

func TestCheckPermissionsBasic(t *testing.T) {
	t.Setenv("USER", "tester")
	info := CheckPermissions("linux")
	if info.Username != "tester" {
		t.Fatalf("expected username tester, got %q", info.Username)
	}
}
