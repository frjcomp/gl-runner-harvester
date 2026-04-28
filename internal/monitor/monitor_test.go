package monitor

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/frjcomp/gl-runner-harvester/internal/detector"
)

type fakeHarvester struct {
	current []string
	jobs    []string
	procs   []string
}

func (f *fakeHarvester) HarvestJob(jobDir string) error {
	f.jobs = append(f.jobs, jobDir)
	return nil
}

func (f *fakeHarvester) HarvestProcess(jobID string, _ map[string]string, _ string) error {
	f.procs = append(f.procs, jobID)
	return nil
}

func TestNew(t *testing.T) {
	h := &fakeHarvester{}
	m := New(detector.OSInfo{OS: "linux"}, detector.Shell, 3, h)
	if m == nil {
		t.Fatalf("expected monitor")
	}
	if m.interval != 3*time.Second {
		t.Fatalf("unexpected interval: %v", m.interval)
	}
}

func TestShellBuildDirs(t *testing.T) {
	linuxDirs := shellBuildDirs("linux")
	if len(linuxDirs) < 2 {
		t.Fatalf("expected linux dirs")
	}

	winDirs := shellBuildDirs("windows")
	if len(winDirs) != 1 {
		t.Fatalf("expected one windows dir")
	}
}

func TestDockerAndKubernetesBuildDirs(t *testing.T) {
	docker := dockerBuildDirs()
	if len(docker) == 0 {
		t.Fatalf("expected docker dirs")
	}

	t.Setenv("CI_PROJECT_DIR", "/tmp/proj/sub")
	k8s := kubernetesBuildDirs()
	if !contains(k8s, "/tmp/proj") {
		t.Fatalf("expected project parent in k8s dirs: %v", k8s)
	}
}

func TestFilterExisting(t *testing.T) {
	tmp := t.TempDir()
	none := filepath.Join(tmp, "missing")
	out := filterExisting([]string{tmp, none})
	if len(out) != 1 {
		t.Fatalf("expected one existing dir, got %v", out)
	}
	if runtime.GOOS != "windows" && out[0] != tmp {
		t.Fatalf("unexpected output: %v", out)
	}
}

func TestContains(t *testing.T) {
	if !contains([]string{"a", "b"}, "b") {
		t.Fatalf("expected true")
	}
	if contains([]string{"a"}, "b") {
		t.Fatalf("expected false")
	}
}

func TestPollHarvestsNewDirectories(t *testing.T) {
	tmp := t.TempDir()
	jobDir := filepath.Join(tmp, "job1")
	if err := os.MkdirAll(jobDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	h := &fakeHarvester{}
	m := &Monitor{h: h, seen: map[string]struct{}{}}
	m.poll([]string{tmp})

	if len(h.jobs) != 1 {
		t.Fatalf("expected one harvested job, got %d", len(h.jobs))
	}
	if h.jobs[0] != jobDir {
		t.Fatalf("unexpected job dir: %s", h.jobs[0])
	}

	// Already seen should not re-harvest.
	m.poll([]string{tmp})
	if len(h.jobs) != 1 {
		t.Fatalf("expected no duplicate harvests")
	}
}

func TestStartLoopCanceled(t *testing.T) {
	h := &fakeHarvester{}
	m := &Monitor{h: h, seen: map[string]struct{}{}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := m.startLoop(ctx, make(chan time.Time), nil)
	if err == nil {
		t.Fatalf("expected context cancellation error")
	}
}

func TestStartWithInjectedCanceledContext(t *testing.T) {
	oldNotify := notifyContext
	defer func() { notifyContext = oldNotify }()

	notifyContext = func(parent context.Context, _ ...os.Signal) (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(parent)
		cancel()
		return ctx, func() {}
	}

	h := &fakeHarvester{}
	m := New(detector.OSInfo{OS: "linux"}, detector.Unknown, 1, h)
	err := m.Start()
	if err == nil {
		t.Fatalf("expected cancellation error")
	}
}

func TestPollProcessesHarvestsNewJobIDs(t *testing.T) {
	h := &fakeHarvester{}
	m := &Monitor{
		h:    h,
		seen: map[string]struct{}{},
		listProc: func() ([]processJob, error) {
			return []processJob{
				{PID: 11, JobID: "101", Cmdline: "bash", Env: map[string]string{"CI_JOB_ID": "101"}},
				{PID: 12, JobID: "102", Cmdline: "bash", Env: map[string]string{"CI_JOB_ID": "102"}},
			}, nil
		},
	}

	m.pollProcesses()
	if len(h.procs) != 2 {
		t.Fatalf("expected two harvested process jobs, got %d", len(h.procs))
	}

	// Duplicate job IDs should not be harvested again.
	m.listProc = func() ([]processJob, error) {
		return []processJob{{PID: 99, JobID: "101", Cmdline: "bash", Env: map[string]string{"CI_JOB_ID": "101"}}}, nil
	}
	m.pollProcesses()
	if len(h.procs) != 2 {
		t.Fatalf("expected deduped process jobs, got %d", len(h.procs))
	}
}

func TestNewUsesProcessListerForLinuxShell(t *testing.T) {
	h := &fakeHarvester{}
	m := New(detector.OSInfo{OS: "linux"}, detector.Shell, 1, h)
	if m.listProc == nil {
		t.Fatalf("expected process lister for linux shell")
	}

	m = New(detector.OSInfo{OS: "linux"}, detector.Docker, 1, h)
	if m.listProc != nil {
		t.Fatalf("did not expect process lister for linux docker")
	}
}

func TestNewUsesProcessListerForWindowsShell(t *testing.T) {
	h := &fakeHarvester{}
	m := New(detector.OSInfo{OS: "windows"}, detector.Shell, 1, h)
	if m.listProc == nil {
		t.Fatalf("expected process lister for windows shell")
	}

	m = New(detector.OSInfo{OS: "windows"}, detector.Docker, 1, h)
	if m.listProc != nil {
		t.Fatalf("did not expect process lister for windows docker")
	}
}

func TestIsGitLabRunnerUser(t *testing.T) {
	if !isGitLabRunnerUser("HOST\\gitlab-runner") {
		t.Fatalf("expected domain-qualified gitlab-runner to match")
	}
	if !isGitLabRunnerUser("gitlab-runner") {
		t.Fatalf("expected plain gitlab-runner to match")
	}
	if isGitLabRunnerUser("HOST\\someone") {
		t.Fatalf("did not expect non-runner account to match")
	}
}

func TestCILookupCaseInsensitive(t *testing.T) {
	env := map[string]string{"ci_job_id": "777"}
	if got := ciLookup(env, "CI_JOB_ID"); got != "777" {
		t.Fatalf("expected case-insensitive match, got %q", got)
	}

	if got := ciLookup(map[string]string{"OTHER": "x"}, "CI_JOB_ID"); got != "" {
		t.Fatalf("expected empty when key is missing, got %q", got)
	}
}
