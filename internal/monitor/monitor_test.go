package monitor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/frjcomp/gl-runner-harvester/internal/detector"
)

type fakeHarvester struct {
	jobs  []string
	procs []string
}

func (f *fakeHarvester) HarvestJob(jobDir string) error {
	f.jobs = append(f.jobs, jobDir)
	return nil
}

func (f *fakeHarvester) HarvestProcess(jobID string, _ map[string]string, _ string) error {
	f.procs = append(f.procs, jobID)
	return nil
}

type fakeStrategy struct {
	mode string
	jobs []discoveredJob
	err  error
}

func (f *fakeStrategy) Mode() string {
	return f.mode
}

func (f *fakeStrategy) Discover(context.Context) ([]discoveredJob, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.jobs, nil
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
	if m.strategy == nil {
		t.Fatalf("expected strategy")
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
	m := &Monitor{
		h:        h,
		seen:     map[string]struct{}{},
		strategy: &directoryDiscoveryStrategy{mode: "test-dir", dirs: []string{tmp}},
	}
	m.poll(context.Background())

	if len(h.jobs) != 1 {
		t.Fatalf("expected one harvested job, got %d", len(h.jobs))
	}
	if h.jobs[0] != jobDir {
		t.Fatalf("unexpected job dir: %s", h.jobs[0])
	}

	m.poll(context.Background())
	if len(h.jobs) != 1 {
		t.Fatalf("expected no duplicate harvests")
	}
}

func TestPollHarvestsNewProcessJobs(t *testing.T) {
	h := &fakeHarvester{}
	m := &Monitor{
		h:    h,
		seen: map[string]struct{}{},
		strategy: &processDiscoveryStrategy{
			mode: "test-proc",
			lister: func() ([]processJob, error) {
				return []processJob{
					{PID: 11, JobID: "101", Cmdline: "bash", Env: map[string]string{"CI_JOB_ID": "101"}},
					{PID: 12, JobID: "102", Cmdline: "bash", Env: map[string]string{"CI_JOB_ID": "102"}},
				}, nil
			},
		},
	}

	m.poll(context.Background())
	if len(h.procs) != 2 {
		t.Fatalf("expected two harvested process jobs, got %d", len(h.procs))
	}

	m.strategy = &processDiscoveryStrategy{
		mode: "test-proc",
		lister: func() ([]processJob, error) {
			return []processJob{{PID: 99, JobID: "101", Cmdline: "bash", Env: map[string]string{"CI_JOB_ID": "101"}}}, nil
		},
	}
	m.poll(context.Background())
	if len(h.procs) != 2 {
		t.Fatalf("expected deduped process jobs, got %d", len(h.procs))
	}
}

func TestPollHandlesStrategyError(t *testing.T) {
	h := &fakeHarvester{}
	m := &Monitor{
		h:        h,
		seen:     map[string]struct{}{},
		strategy: &fakeStrategy{mode: "test", err: fmt.Errorf("boom")},
	}

	m.poll(context.Background())
	if len(h.jobs) != 0 || len(h.procs) != 0 {
		t.Fatalf("did not expect harvest calls on strategy error")
	}
}

func TestStartLoopCanceled(t *testing.T) {
	h := &fakeHarvester{}
	m := &Monitor{
		h:        h,
		seen:     map[string]struct{}{},
		strategy: &fakeStrategy{mode: "test"},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := m.startLoop(ctx, make(chan time.Time))
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

func TestNewUsesProcessStrategyForLinuxShell(t *testing.T) {
	h := &fakeHarvester{}
	m := New(detector.OSInfo{OS: "linux"}, detector.Shell, 1, h)
	if m.strategy == nil || m.strategy.Mode() != "shell-proc-linux" {
		t.Fatalf("expected linux shell process strategy, got %v", m.strategy)
	}
}

func TestNewUsesProcessStrategyForWindowsShell(t *testing.T) {
	h := &fakeHarvester{}
	m := New(detector.OSInfo{OS: "windows"}, detector.Shell, 1, h)
	if m.strategy == nil || m.strategy.Mode() != "shell-proc-windows" {
		t.Fatalf("expected windows shell process strategy, got %v", m.strategy)
	}
}

func TestDockerStrategyFallbackToDirectory(t *testing.T) {
	oldFactory := newDockerStrategy
	defer func() { newDockerStrategy = oldFactory }()

	newDockerStrategy = func(detector.OSInfo) (*strategyWithCloser, error) {
		return nil, fmt.Errorf("no daemon")
	}

	h := &fakeHarvester{}
	m := New(detector.OSInfo{OS: "linux"}, detector.Docker, 1, h)
	if m.strategy == nil || m.strategy.Mode() != "docker-dir-fallback" {
		t.Fatalf("expected docker directory fallback strategy, got %v", m.strategy)
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
