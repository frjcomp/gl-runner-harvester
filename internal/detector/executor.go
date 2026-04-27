package detector

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/rs/zerolog/log"
)

// ExecutorType represents the type of GitLab runner executor.
type ExecutorType string

const (
	Shell      ExecutorType = "shell"
	SSH        ExecutorType = "ssh"
	Docker     ExecutorType = "docker"
	Kubernetes ExecutorType = "kubernetes"
	Unknown    ExecutorType = "unknown"
)

// DetectExecutor determines the GitLab runner executor type and returns
// related metadata discovered during detection.
func DetectExecutor() (ExecutorType, map[string]string) {
	meta := make(map[string]string)

	// Explicit executor env var set by some runner configurations.
	if v := os.Getenv("CI_RUNNER_EXECUTOR"); v != "" {
		meta["source"] = "CI_RUNNER_EXECUTOR"
		meta["value"] = v
		return normalizeExecutor(v), meta
	}

	// Kubernetes: check for the service host env var injected into every pod.
	if v := os.Getenv("KUBERNETES_SERVICE_HOST"); v != "" {
		meta["source"] = "KUBERNETES_SERVICE_HOST"
		meta["kubernetes_service_host"] = v
		return Kubernetes, meta
	}

	// Docker: check for /.dockerenv (created by Docker daemon) or cgroup hint.
	if isInsideDocker() {
		meta["source"] = "docker_detection"
		return Docker, meta
	}

	// CI_SHARED_ENVIRONMENT indicates shell or SSH executor.
	if os.Getenv("CI_SHARED_ENVIRONMENT") == "true" {
		meta["source"] = "CI_SHARED_ENVIRONMENT"
		// SSH executor is a sub-type of shell; we distinguish it here if possible.
		if os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_CLIENT") != "" {
			meta["ssh_detected"] = "true"
			return SSH, meta
		}
		return Shell, meta
	}

	// Attempt to parse the runner config.toml for executor type.
	if execType, found := detectFromConfigToml(meta); found {
		return execType, meta
	}

	// If we are inside a CI environment but could not determine the type,
	// fall back to shell as the most common case.
	if os.Getenv("CI") == "true" || os.Getenv("GITLAB_CI") == "true" {
		meta["source"] = "CI_env_fallback"
		return Shell, meta
	}

	return Unknown, meta
}

func normalizeExecutor(v string) ExecutorType {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "shell":
		return Shell
	case "ssh":
		return SSH
	case "docker", "docker+machine":
		return Docker
	case "kubernetes":
		return Kubernetes
	default:
		return Unknown
	}
}

func isInsideDocker() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}

	data, err := os.ReadFile("/proc/1/cgroup")
	if err != nil {
		return false
	}
	content := string(data)
	return strings.Contains(content, "docker") || strings.Contains(content, "containerd")
}

// detectFromConfigToml tries to read the GitLab runner config.toml and parse the executor type.
func detectFromConfigToml(meta map[string]string) (ExecutorType, bool) {
	candidates := configTomlPaths()
	for _, p := range candidates {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		log.Debug().Str("config", p).Msg("Found gitlab-runner config.toml")
		meta["config_path"] = p
		content := string(data)
		for _, line := range strings.Split(content, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "executor") {
				parts := strings.SplitN(line, "=", 2)
				if len(parts) == 2 {
					val := strings.Trim(strings.TrimSpace(parts[1]), `"`)
					meta["source"] = "config.toml"
					meta["executor_value"] = val
					return normalizeExecutor(val), true
				}
			}
		}
	}
	return Unknown, false
}

func configTomlPaths() []string {
	paths := []string{
		"/etc/gitlab-runner/config.toml",
	}

	if runtime.GOOS == "windows" {
		paths = append(paths, `C:\GitLab-Runner\config.toml`)
	}

	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".gitlab-runner", "config.toml"))
	}

	return paths
}
