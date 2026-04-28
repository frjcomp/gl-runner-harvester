package detector

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// PermissionInfo holds details about the current process privileges.
type PermissionInfo struct {
	IsPrivileged         bool
	Username             string
	UID                  int
	RunnerBinaryPath     string
	RunnerBinaryWritable bool
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

	return info
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
