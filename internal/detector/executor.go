package detector

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/BurntSushi/toml"
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

// Config represents the GitLab runner configuration structure.
type Config struct {
	Runners []Runner `toml:"runners"`
}

var (
	detectorFileExists     = fileExists
	detectorDirExists      = dirExists
	detectorCgroupContains = cgroupContains
	detectorUserHomeDir    = os.UserHomeDir
)

// Runner represents a single runner configuration in the TOML.
type Runner struct {
	Name     string `toml:"name"`
	URL      string `toml:"url"`
	Executor string `toml:"executor"`
}

// DetectExecutor determines the GitLab runner executor type by parsing the config file.
// If configPath is empty, it searches standard locations for the config.toml.
func DetectExecutor(configPath string) (ExecutorType, map[string]string) {
	meta := make(map[string]string)

	// If no config path provided, find one
	if configPath == "" {
		if path, found, denied := findConfigToml(); found {
			configPath = path
		} else {
			searched := configTomlCandidates()
			if len(denied) > 0 {
				meta["config_reason"] = "runner_config_permission_denied"
				meta["permission_denied_paths"] = strings.Join(denied, ",")
				log.Debug().
					Strs("permission_denied_paths", denied).
					Msg("GitLab runner config.toml exists but is not readable due to permissions; attempting disk artifact executor detection")
				return detectExecutorFromArtifacts(meta)
			}

			meta["config_reason"] = "runner_config_not_found"
			meta["searched_paths"] = strings.Join(searched, ",")
			log.Debug().
				Strs("searched_paths", searched).
				Msg("GitLab runner config.toml not found; attempting disk artifact executor detection")
			return detectExecutorFromArtifacts(meta)
		}
	}

	// Try to parse the config file
	if configPath != "" {
		if execType, found, err := parseConfigToml(configPath, meta); found {
			return execType, meta
		} else if err != nil && errors.Is(err, os.ErrPermission) {
			meta["config_reason"] = "runner_config_permission_denied"
			meta["config_path"] = configPath
			log.Debug().
				Str("config", configPath).
				Msg("GitLab runner config.toml is not readable due to permissions; attempting disk artifact executor detection")
			return detectExecutorFromArtifacts(meta)
		}

		meta["config_reason"] = "runner_config_parse_failed"
		meta["config_path"] = configPath
		log.Warn().
			Str("config", configPath).
			Msg("Failed to parse GitLab runner config.toml; attempting disk artifact executor detection")
		return detectExecutorFromArtifacts(meta)
	}

	return detectExecutorFromArtifacts(meta)
}

// parseConfigToml reads and parses the GitLab runner config.toml file as TOML.
func parseConfigToml(configPath string, meta map[string]string) (ExecutorType, bool, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		log.Debug().Err(err).Str("config", configPath).Msg("Failed to read config file")
		return Unknown, false, err
	}

	log.Warn().
		Str("config", configPath).
		Msg("GitLab runner config.toml is readable by current user; this is usually unexpected")

	var config Config
	if err := toml.Unmarshal(data, &config); err != nil {
		log.Debug().Err(err).Str("config", configPath).Msg("Failed to parse TOML config")
		return Unknown, false, err
	}

	log.Debug().Str("config", configPath).Int("runners", len(config.Runners)).Msg("Parsed gitlab-runner config.toml")
	meta["config_path"] = configPath
	meta["source"] = "config.toml"

	if len(config.Runners) > 1 {
		log.Warn().
			Str("config", configPath).
			Int("runners", len(config.Runners)).
			Msg("Multiple runners detected in config.toml; only the first runner is used")
	}

	// Get executor from first runner
	if len(config.Runners) > 0 {
		runner := config.Runners[0]
		if runner.Executor != "" {
			meta["executor_value"] = runner.Executor
			meta["runner_name"] = runner.Name
			return normalizeExecutor(runner.Executor), true, nil
		}
	}

	return Unknown, false, nil
}

// findConfigToml searches standard locations for the config.toml file.
func findConfigToml() (string, bool, []string) {
	candidates := configTomlCandidates()
	permissionDenied := make([]string, 0)

	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, true, nil
		} else if errors.Is(err, os.ErrPermission) {
			permissionDenied = append(permissionDenied, p)
		}
	}

	return "", false, permissionDenied
}

func configTomlCandidates() []string {
	candidates := []string{
		"/etc/gitlab-runner/config.toml",
	}

	if runtime.GOOS == "windows" {
		candidates = append(candidates, `C:\GitLab-Runner\config.toml`)
	}

	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".gitlab-runner", "config.toml"))
	}

	return candidates
}

func detectExecutorFromArtifacts(meta map[string]string) (ExecutorType, map[string]string) {
	if artifacts, confidence := kubernetesArtifacts(); len(artifacts) >= 2 {
		meta["source"] = "disk_artifacts"
		meta["reason"] = "kubernetes_artifacts_detected"
		meta["confidence"] = confidence
		meta["artifacts_found"] = strings.Join(artifacts, ",")
		return Kubernetes, meta
	}

	if artifacts, confidence := dockerArtifacts(); len(artifacts) >= 2 {
		meta["source"] = "disk_artifacts"
		meta["reason"] = "docker_artifacts_detected"
		meta["confidence"] = confidence
		meta["artifacts_found"] = strings.Join(artifacts, ",")
		return Docker, meta
	}

	if artifacts, confidence := shellArtifacts(); len(artifacts) > 0 {
		meta["source"] = "disk_artifacts"
		meta["reason"] = "shell_host_artifacts_detected"
		meta["confidence"] = confidence
		meta["artifacts_found"] = strings.Join(artifacts, ",")
		return Shell, meta
	}

	meta["source"] = "disk_artifacts"
	meta["reason"] = "insufficient_disk_markers"
	meta["confidence"] = "low"
	log.Warn().Msg("Could not determine executor from disk artifacts; using unknown executor")
	return Unknown, meta
}

func kubernetesArtifacts() ([]string, string) {
	artifacts := make([]string, 0, 4)
	for _, path := range []string{
		"/var/run/secrets/kubernetes.io/serviceaccount/token",
		"/var/run/secrets/kubernetes.io/serviceaccount/namespace",
		"/var/run/secrets/kubernetes.io/serviceaccount/ca.crt",
	} {
		if detectorFileExists(path) {
			artifacts = append(artifacts, path)
		}
	}

	if detectorCgroupContains("kubepods") {
		artifacts = append(artifacts, "/proc/1/cgroup:kubepods")
	}

	confidence := "medium"
	if len(artifacts) >= 3 {
		confidence = "high"
	}

	return artifacts, confidence
}

func dockerArtifacts() ([]string, string) {
	artifacts := make([]string, 0, 3)
	if detectorFileExists("/.dockerenv") {
		artifacts = append(artifacts, "/.dockerenv")
	}
	if detectorDirExists("/builds") {
		artifacts = append(artifacts, "/builds")
	}
	if detectorCgroupContains("docker") || detectorCgroupContains("containerd") {
		artifacts = append(artifacts, "/proc/1/cgroup:container")
	}

	confidence := "medium"
	if len(artifacts) >= 2 && containsString(artifacts, "/.dockerenv") && containsString(artifacts, "/builds") {
		confidence = "high"
	}

	return artifacts, confidence
}

func shellArtifacts() ([]string, string) {
	artifacts := make([]string, 0, 6)
	for _, path := range []string{
		"/var/lib/gitlab-runner/builds",
		"/home/gitlab-runner/builds",
		"/etc/gitlab-runner",
		"/usr/bin/gitlab-runner",
		"/usr/local/bin/gitlab-runner",
	} {
		if path == "/etc/gitlab-runner" || strings.HasSuffix(path, "/builds") {
			if detectorDirExists(path) {
				artifacts = append(artifacts, path)
			}
			continue
		}
		if detectorFileExists(path) {
			artifacts = append(artifacts, path)
		}
	}

	if home, err := detectorUserHomeDir(); err == nil {
		userConfig := filepath.Join(home, ".gitlab-runner", "config.toml")
		if detectorFileExists(userConfig) {
			artifacts = append(artifacts, userConfig)
		}
	}

	confidence := "medium"
	if containsSuffix(artifacts, "/builds") {
		confidence = "high"
	}

	return artifacts, confidence
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func cgroupContains(marker string) bool {
	data, err := os.ReadFile("/proc/1/cgroup")
	if err != nil {
		return false
	}
	return strings.Contains(string(data), marker)
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsSuffix(values []string, suffix string) bool {
	for _, value := range values {
		if strings.HasSuffix(value, suffix) {
			return true
		}
	}
	return false
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
