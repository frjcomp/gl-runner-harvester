package harvester

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeriveJobID(t *testing.T) {
	t.Setenv("CI_JOB_ID", "123")
	if got := deriveJobID("/tmp/job"); got != "123" {
		t.Fatalf("expected CI_JOB_ID, got %q", got)
	}

	t.Setenv("CI_JOB_ID", "")
	if got := deriveJobID("/tmp/job"); got != "job" {
		t.Fatalf("expected basename fallback, got %q", got)
	}
}

func TestCollectEnvVarsAndCIVars(t *testing.T) {
	t.Setenv("CI_SAMPLE", "a")
	t.Setenv("GITLAB_TOKEN", "b")
	t.Setenv("OTHER_SAMPLE", "c")

	env := collectEnvVars()
	if env["OTHER_SAMPLE"] != "c" {
		t.Fatalf("expected OTHER_SAMPLE in env map")
	}

	ci := collectCIVars()
	if ci["CI_SAMPLE"] != "a" {
		t.Fatalf("expected CI var")
	}
	if ci["GITLAB_TOKEN"] != "b" {
		t.Fatalf("expected GITLAB var")
	}
	if _, ok := ci["OTHER_SAMPLE"]; ok {
		t.Fatalf("did not expect OTHER_SAMPLE in CI vars")
	}
}

func TestCopyFileAndDir(t *testing.T) {
	tmp := t.TempDir()
	srcFile := filepath.Join(tmp, "src.txt")
	if err := os.WriteFile(srcFile, []byte("hello"), 0o600); err != nil {
		t.Fatalf("write src file: %v", err)
	}

	dstFile := filepath.Join(tmp, "nested", "dst.txt")
	if err := copyFile(srcFile, dstFile); err != nil {
		t.Fatalf("copyFile: %v", err)
	}
	data, err := os.ReadFile(dstFile)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("unexpected copied contents: %q", string(data))
	}

	srcDir := filepath.Join(tmp, "srcdir")
	if err := os.MkdirAll(filepath.Join(srcDir, "sub"), 0o700); err != nil {
		t.Fatalf("mkdir src dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "sub", "f"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write nested file: %v", err)
	}

	dstDir := filepath.Join(tmp, "dstdir")
	if err := copyDir(srcDir, dstDir); err != nil {
		t.Fatalf("copyDir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dstDir, "sub", "f")); err != nil {
		t.Fatalf("expected copied nested file: %v", err)
	}
}

func TestWriteSummary(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "summary.json")
	data := JobData{JobID: "1", EnvVars: map[string]string{"A": "B"}}

	if err := writeSummary(path, data); err != nil {
		t.Fatalf("writeSummary: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read summary: %v", err)
	}
	var got JobData
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal summary: %v", err)
	}
	if got.JobID != "1" {
		t.Fatalf("unexpected job id: %q", got.JobID)
	}
}

func TestHarvestCredentialFiles(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	dest := t.TempDir()

	t.Setenv("HOME", home)

	if err := os.WriteFile(filepath.Join(home, ".netrc"), []byte("machine"), 0o600); err != nil {
		t.Fatalf("write .netrc: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".env"), []byte("A=B"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	oldWd, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(oldWd) }()

	found := harvestCredentialFiles(dest)
	joined := strings.Join(found, ",")
	if !strings.Contains(joined, ".netrc") {
		t.Fatalf("expected .netrc in found set: %v", found)
	}
	if !strings.Contains(joined, ".env") {
		t.Fatalf("expected .env in found set: %v", found)
	}
}

func TestHarvestMethodsNoScan(t *testing.T) {
	out := t.TempDir()
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "f.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write source file: %v", err)
	}

	h := New(out, false, "")
	if err := h.HarvestJob(src); err != nil {
		t.Fatalf("HarvestJob: %v", err)
	}

	t.Setenv("CI_PROJECT_DIR", src)
	if err := h.HarvestCurrentEnv("job-1"); err != nil {
		t.Fatalf("HarvestCurrentEnv: %v", err)
	}
}
