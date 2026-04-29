package harvester

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDeriveJobID(t *testing.T) {
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

	ci := collectCIVarsFromMap(env)
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

func TestHarvestMethodsNoScan(t *testing.T) {
	out := t.TempDir()
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "f.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write source file: %v", err)
	}

	h := New(Config{OutputDir: out, ScanSecrets: false, GitLabURL: "", HarvestFiles: true})
	if err := h.HarvestJob(src); err != nil {
		t.Fatalf("HarvestJob: %v", err)
	}
}

func TestHarvestProcessSummaryContainsEnvAndCIVars(t *testing.T) {
	out := t.TempDir()
	h := New(Config{OutputDir: out, ScanSecrets: false, GitLabURL: "", HarvestFiles: true})

	env := map[string]string{
		"CI_JOB_ID":      "222",
		"CI_PROJECT_DIR": t.TempDir(),
		"OTHER_KEY":      "value",
	}

	if err := h.HarvestProcess("222", env, "powershell -File job.ps1"); err != nil {
		t.Fatalf("HarvestProcess: %v", err)
	}

	dirs, err := filepath.Glob(filepath.Join(out, "222_*"))
	if err != nil || len(dirs) != 1 {
		t.Fatalf("expected one output dir, got %v (err=%v)", dirs, err)
	}

	summaryRaw, err := os.ReadFile(filepath.Join(dirs[0], "summary.json"))
	if err != nil {
		t.Fatalf("read summary.json: %v", err)
	}
	var got JobData
	if err := json.Unmarshal(summaryRaw, &got); err != nil {
		t.Fatalf("unmarshal summary: %v", err)
	}
	if got.EnvVars["OTHER_KEY"] != "value" {
		t.Fatalf("expected full env map to be persisted")
	}
	if got.CIVars["CI_JOB_ID"] != "222" {
		t.Fatalf("expected CI vars to be persisted")
	}
	if _, ok := got.CIVars["OTHER_KEY"]; ok {
		t.Fatalf("did not expect non-CI key in ci vars snapshot")
	}
}

func TestHarvestProcessNoHarvestFilesWritesNoArtifacts(t *testing.T) {
	out := t.TempDir()
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "f.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write source file: %v", err)
	}

	h := New(Config{OutputDir: out, ScanSecrets: true, GitLabURL: "https://gitlab.com", HarvestFiles: false})
	env := map[string]string{
		"CI_JOB_ID":      "333",
		"CI_PROJECT_DIR": src,
	}
	if err := h.HarvestProcess("333", env, "bash -lc run"); err != nil {
		t.Fatalf("HarvestProcess: %v", err)
	}

	dirs, err := filepath.Glob(filepath.Join(out, "333_*"))
	if err != nil {
		t.Fatalf("glob output: %v", err)
	}
	if len(dirs) != 0 {
		t.Fatalf("expected no harvested artifacts in scan-only mode, got %v", dirs)
	}
}
