package detector

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseConfigTomlValid(t *testing.T) {
	tmp := t.TempDir()
	config := filepath.Join(tmp, "config.toml")
	content := `[[runners]]
name = "runner-1"
executor = "docker"
`
	if err := os.WriteFile(config, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	meta := map[string]string{}
	execType, found, err := parseConfigToml(config, meta)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatalf("expected executor to be found")
	}
	if execType != Docker {
		t.Fatalf("expected docker, got %s", execType)
	}
	if meta["runner_name"] != "runner-1" {
		t.Fatalf("expected runner_name in meta")
	}
}

func TestParseConfigTomlInvalid(t *testing.T) {
	tmp := t.TempDir()
	config := filepath.Join(tmp, "config.toml")
	if err := os.WriteFile(config, []byte("[[runners]"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, _, err := parseConfigToml(config, map[string]string{})
	if err == nil {
		t.Fatalf("expected parse error")
	}
}

func TestDetectExecutorWithConfigPath(t *testing.T) {
	tmp := t.TempDir()
	config := filepath.Join(tmp, "config.toml")
	if err := os.WriteFile(config, []byte("[[runners]]\nname=\"x\"\nexecutor=\"shell\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	execType, meta := DetectExecutor(config)
	if execType != Shell {
		t.Fatalf("expected shell, got %s", execType)
	}
	if meta["source"] != "config.toml" {
		t.Fatalf("expected source=config.toml")
	}
}

func TestConfigTomlCandidatesIncludesHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	candidates := configTomlCandidates()
	joined := strings.Join(candidates, ",")
	if !strings.Contains(joined, filepath.Join(home, ".gitlab-runner", "config.toml")) {
		t.Fatalf("expected home candidate in %v", candidates)
	}
}

func TestFileAndDirExists(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "f")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if !fileExists(f) {
		t.Fatalf("expected fileExists true")
	}
	if fileExists(tmp) {
		t.Fatalf("expected fileExists false for dir")
	}
	if !dirExists(tmp) {
		t.Fatalf("expected dirExists true")
	}
}

func TestCgroupContainsUnknownMarker(t *testing.T) {
	if cgroupContains("___this_should_not_exist___") {
		t.Fatalf("expected marker lookup to be false")
	}
}

func TestContainsHelpers(t *testing.T) {
	values := []string{"a", "b", "/tmp/builds"}
	if !containsString(values, "b") {
		t.Fatalf("expected containsString true")
	}
	if containsString(values, "x") {
		t.Fatalf("expected containsString false")
	}
	if !containsSuffix(values, "builds") {
		t.Fatalf("expected containsSuffix true")
	}
	if containsSuffix(values, "other") {
		t.Fatalf("expected containsSuffix false")
	}
}

func TestNormalizeExecutor(t *testing.T) {
	tests := map[string]ExecutorType{
		"shell":          Shell,
		"ssh":            SSH,
		"docker":         Docker,
		"docker+machine": Docker,
		"kubernetes":     Kubernetes,
		"unknown":        Unknown,
	}

	for input, want := range tests {
		if got := normalizeExecutor(input); got != want {
			t.Fatalf("normalizeExecutor(%q): want %s, got %s", input, want, got)
		}
	}
}
