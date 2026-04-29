package monitor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/frjcomp/gl-runner-harvester/internal/detector"
)

type fakeHarvester struct {
	mu             sync.Mutex
	jobs           []string
	procs          []string
	processStarted chan string
	processCtx     chan context.Context
	blockProcess   <-chan struct{}
}

func (f *fakeHarvester) HarvestJob(_ context.Context, jobDir string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.jobs = append(f.jobs, jobDir)
	return nil
}

func (f *fakeHarvester) HarvestProcess(ctx context.Context, jobID string, _ map[string]string, _ string) error {
	if f.processStarted != nil {
		f.processStarted <- jobID
	}
	if f.processCtx != nil {
		f.processCtx <- ctx
	}
	if f.blockProcess != nil {
		<-f.blockProcess
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.procs = append(f.procs, jobID)
	return nil
}

func (f *fakeHarvester) jobCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.jobs)
}

func (f *fakeHarvester) firstJob() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.jobs) == 0 {
		return ""
	}
	return f.jobs[0]
}

func (f *fakeHarvester) processCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.procs)
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
	m.waitForHarvests()

	if h.jobCount() != 1 {
		t.Fatalf("expected one harvested job, got %d", h.jobCount())
	}
	if h.firstJob() != jobDir {
		t.Fatalf("unexpected job dir: %s", h.firstJob())
	}

	m.poll(context.Background())
	m.waitForHarvests()
	if h.jobCount() != 1 {
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
	m.waitForHarvests()
	if h.processCount() != 2 {
		t.Fatalf("expected two harvested process jobs, got %d", h.processCount())
	}

	m.strategy = &processDiscoveryStrategy{
		mode: "test-proc",
		lister: func() ([]processJob, error) {
			return []processJob{{PID: 99, JobID: "101", Cmdline: "bash", Env: map[string]string{"CI_JOB_ID": "101"}}}, nil
		},
	}
	m.poll(context.Background())
	m.waitForHarvests()
	if h.processCount() != 2 {
		t.Fatalf("expected deduped process jobs, got %d", len(h.procs))
	}
}

func TestPollDoesNotBlockOnInFlightHarvest(t *testing.T) {
	block := make(chan struct{})
	h := &fakeHarvester{processStarted: make(chan string, 2), blockProcess: block}
	m := &Monitor{
		h:    h,
		seen: map[string]struct{}{},
		strategy: &fakeStrategy{mode: "test", jobs: []discoveredJob{{
			Identity:      "job:101",
			JobID:         "101",
			Cmdline:       "bash",
			Env:           map[string]string{"CI_JOB_ID": "101"},
			DiscoveryMode: "test",
		}}},
	}

	m.poll(context.Background())
	select {
	case got := <-h.processStarted:
		if got != "101" {
			t.Fatalf("expected first started job 101, got %q", got)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for first harvest to start")
	}

	m.strategy = &fakeStrategy{mode: "test", jobs: []discoveredJob{{
		Identity:      "job:102",
		JobID:         "102",
		Cmdline:       "bash",
		Env:           map[string]string{"CI_JOB_ID": "102"},
		DiscoveryMode: "test",
	}}}
	m.poll(context.Background())

	select {
	case got := <-h.processStarted:
		if got != "102" {
			t.Fatalf("expected second started job 102, got %q", got)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("second poll did not dispatch while first harvest was still blocked")
	}

	close(block)
	m.waitForHarvests()
	if h.processCount() != 2 {
		t.Fatalf("expected both process harvests to complete, got %d", h.processCount())
	}
}

func TestPollPassesContextToHarvestProcess(t *testing.T) {
	h := &fakeHarvester{processCtx: make(chan context.Context, 1)}
	m := &Monitor{
		h:    h,
		seen: map[string]struct{}{},
		strategy: &fakeStrategy{mode: "test", jobs: []discoveredJob{{
			Identity:      "job:101",
			JobID:         "101",
			Cmdline:       "bash",
			Env:           map[string]string{"CI_JOB_ID": "101"},
			DiscoveryMode: "test",
		}}},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.poll(ctx)

	var harvestCtx context.Context
	select {
	case harvestCtx = <-h.processCtx:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for harvest context")
	}

	cancel()
	select {
	case <-harvestCtx.Done():
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("harvest context was not canceled")
	}

	m.waitForHarvests()
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

func TestJobWebURL(t *testing.T) {
	tests := []struct {
		name  string
		env   map[string]string
		jobID string
		want  string
	}{
		{
			name: "uses CI_JOB_URL directly",
			env: map[string]string{
				"CI_JOB_URL": "https://gitlab.example.com/group/project/-/jobs/123",
			},
			jobID: "123",
			want:  "https://gitlab.example.com/group/project/-/jobs/123",
		},
		{
			name: "builds URL from server project and env job id",
			env: map[string]string{
				"CI_SERVER_URL":   "https://gitlab.example.com/",
				"CI_PROJECT_PATH": "group/project",
				"CI_JOB_ID":       "456",
			},
			jobID: "ignored",
			want:  "https://gitlab.example.com/group/project/-/jobs/456",
		},
		{
			name: "falls back to discovered job id",
			env: map[string]string{
				"CI_SERVER_URL":   "https://gitlab.example.com",
				"CI_PROJECT_PATH": "group/project",
			},
			jobID: "789",
			want:  "https://gitlab.example.com/group/project/-/jobs/789",
		},
		{
			name: "returns empty when missing metadata",
			env: map[string]string{
				"CI_SERVER_URL": "https://gitlab.example.com",
			},
			jobID: "789",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := jobWebURL(tt.env, tt.jobID); got != tt.want {
				t.Fatalf("jobWebURL() = %q, want %q", got, tt.want)
			}
		})
	}
}
