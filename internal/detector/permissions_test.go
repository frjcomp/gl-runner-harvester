package detector

import (
	"net"
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

func TestDetectDockerHostDefaults(t *testing.T) {
	t.Setenv("DOCKER_HOST", "")
	if got := detectDockerHost("linux"); got != "unix:///var/run/docker.sock" {
		t.Fatalf("unexpected linux default docker host: %q", got)
	}
	if got := detectDockerHost("windows"); got != "npipe:////./pipe/docker_engine" {
		t.Fatalf("unexpected windows default docker host: %q", got)
	}

	t.Setenv("DOCKER_HOST", "unix:///tmp/custom.sock")
	if got := detectDockerHost("linux"); got != "unix:///tmp/custom.sock" {
		t.Fatalf("expected env override docker host, got %q", got)
	}
}

func TestCanAccessDockerDaemonUnix(t *testing.T) {
	if canAccessDockerDaemon("linux", "unix:///tmp/does-not-exist.sock") {
		t.Fatalf("expected missing unix socket to be inaccessible")
	}

	tmp := t.TempDir()
	sock := filepath.Join(tmp, "docker.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	defer ln.Close()

	if !canAccessDockerDaemon("linux", "unix://"+sock) {
		t.Fatalf("expected unix socket daemon to be accessible")
	}
}

func TestCanAccessDockerDaemonTCP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen tcp: %v", err)
	}
	defer ln.Close()

	if !canAccessDockerDaemon("linux", "tcp://"+ln.Addr().String()) {
		t.Fatalf("expected tcp daemon endpoint to be accessible")
	}

	if canAccessDockerDaemon("linux", "tcp://127.0.0.1:1") {
		t.Fatalf("expected unreachable tcp endpoint to be inaccessible")
	}
}
