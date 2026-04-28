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
}

func (f *fakeHarvester) HarvestCurrentEnv(jobID string) error {
	f.current = append(f.current, jobID)
	return nil
}

func (f *fakeHarvester) HarvestJob(jobDir string) error {
	f.jobs = append(f.jobs, jobDir)
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
