package detector

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// PermissionInfo holds details about the current process privileges.
type PermissionInfo struct {
	IsPrivileged         bool
	Username             string
	UID                  int
	RunnerBinaryPath     string
	RunnerBinaryWritable bool
	DockerHost           string
	DockerDaemonReadable bool
}

// CheckPermissions inspects process privileges for the given OS.
func CheckPermissions(goos string) PermissionInfo {
	info := PermissionInfo{}
	info.Username = currentUsername()

	switch goos {
	case "windows":
		info.IsPrivileged = isWindowsAdmin()
	default:
		info.UID = os.Getuid()
		info.IsPrivileged = info.UID == 0
	}

	info.RunnerBinaryPath = findRunnerBinary(goos)
	if info.RunnerBinaryPath != "" {
		info.RunnerBinaryWritable = isWritable(info.RunnerBinaryPath)
	}

	info.DockerHost = detectDockerHost(goos)
	info.DockerDaemonReadable = canAccessDockerDaemon(goos, info.DockerHost)

	return info
}

func detectDockerHost(goos string) string {
	if v := strings.TrimSpace(os.Getenv("DOCKER_HOST")); v != "" {
		return v
	}
	if goos == "windows" {
		return "npipe:////./pipe/docker_engine"
	}
	return "unix:///var/run/docker.sock"
}

func canAccessDockerDaemon(goos, host string) bool {
	if strings.HasPrefix(host, "unix://") {
		path := strings.TrimPrefix(host, "unix://")
		if path == "" {
			return false
		}
		if _, err := os.Stat(path); err != nil {
			return false
		}
		conn, err := net.DialTimeout("unix", path, 1500*time.Millisecond)
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	}

	if strings.HasPrefix(host, "npipe://") {
		pipePath := strings.TrimPrefix(host, "npipe://")
		pipePath = strings.TrimPrefix(pipePath, "/")
		pipePath = filepath.FromSlash(pipePath)
		if goos == "windows" {
			f, err := os.OpenFile(pipePath, os.O_RDWR, 0)
			if err != nil {
				return false
			}
			_ = f.Close()
			return true
		}
		return false
	}

	return false
}

func currentUsername() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	if u := os.Getenv("USERNAME"); u != "" {
		return u
	}
	if u := os.Getenv("LOGNAME"); u != "" {
		return u
	}
	return "unknown"
}

// findRunnerBinary locates the gitlab-runner binary on PATH or common locations.
func findRunnerBinary(goos string) string {
	name := "gitlab-runner"
	if goos == "windows" {
		name = "gitlab-runner.exe"
	}

	if p, err := exec.LookPath(name); err == nil {
		return p
	}

	// Common installation paths.
	candidates := []string{
		"/usr/bin/gitlab-runner",
		"/usr/local/bin/gitlab-runner",
	}
	if goos == "windows" {
		candidates = []string{
			`C:\GitLab-Runner\gitlab-runner.exe`,
		}
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

func isWritable(path string) bool {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

// isWindowsAdmin checks for administrative privileges on Windows by attempting
// to open a privileged handle. We use a command-based approach for portability
// since syscall imports differ between platforms.
func isWindowsAdmin() bool {
	if runtime.GOOS != "windows" {
		return false
	}
	out, err := exec.Command("net", "session").CombinedOutput()
	if err != nil {
		return false
	}
	return !strings.Contains(strings.ToLower(string(out)), "access is denied")
}
