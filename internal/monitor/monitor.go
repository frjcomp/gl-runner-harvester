package monitor

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/frjcomp/gl-runner-harvester/internal/detector"
	"github.com/rs/zerolog/log"
)

// Monitor watches for active GitLab CI/CD jobs and triggers the harvester.
type Monitor struct {
	osInfo   detector.OSInfo
	execType detector.ExecutorType
	interval time.Duration
	h        jobHarvester
	seen     map[string]struct{}
	strategy discoveryStrategy
	closer   io.Closer
}

type jobHarvester interface {
	HarvestJob(jobDir string) error
	HarvestProcess(jobID string, env map[string]string, cmdline string) error
}

type processJob struct {
	PID     int
	JobID   string
	Cmdline string
	Env     map[string]string
}

type processJobLister func() ([]processJob, error)

type discoveredJob struct {
	Identity      string
	JobID         string
	Cmdline       string
	Env           map[string]string
	SourceDir     string
	IsDirectory   bool
	DiscoveryMode string
}

type discoveryStrategy interface {
	Mode() string
	Discover(ctx context.Context) ([]discoveredJob, error)
}

type processDiscoveryStrategy struct {
	mode   string
	lister processJobLister
}

func (p *processDiscoveryStrategy) Mode() string {
	return p.mode
}

func (p *processDiscoveryStrategy) Discover(_ context.Context) ([]discoveredJob, error) {
	jobs, err := p.lister()
	if err != nil {
		return nil, err
	}

	out := make([]discoveredJob, 0, len(jobs))
	for _, job := range jobs {
		jobID := strings.TrimSpace(job.JobID)
		if jobID == "" {
			continue
		}
		out = append(out, discoveredJob{
			Identity:      "job:" + jobID,
			JobID:         jobID,
			Cmdline:       job.Cmdline,
			Env:           job.Env,
			DiscoveryMode: p.mode,
		})
	}
	return out, nil
}

type directoryDiscoveryStrategy struct {
	mode string
	dirs []string
}

func (d *directoryDiscoveryStrategy) Mode() string {
	return d.mode
}

func (d *directoryDiscoveryStrategy) Discover(_ context.Context) ([]discoveredJob, error) {
	out := make([]discoveredJob, 0)
	for _, dir := range d.dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			log.Debug().Err(err).Str("dir", dir).Msg("Cannot read watch directory")
			continue
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			jobDir := filepath.Join(dir, entry.Name())
			out = append(out, discoveredJob{
				Identity:      "dir:" + jobDir,
				JobID:         filepath.Base(jobDir),
				SourceDir:     jobDir,
				IsDirectory:   true,
				DiscoveryMode: d.mode,
			})
		}
	}

	return out, nil
}

type strategyWithCloser struct {
	discoveryStrategy
	closer io.Closer
}

type dockerStrategyFactoryFunc func(osInfo detector.OSInfo) (*strategyWithCloser, error)

var newDockerStrategy dockerStrategyFactoryFunc = defaultDockerStrategy
var notifyContext = signal.NotifyContext

// New creates a new Monitor instance.
func New(osInfo detector.OSInfo, execType detector.ExecutorType, intervalSecs int, h jobHarvester) *Monitor {
	strategy, closer := selectStrategy(osInfo, execType)
	return &Monitor{
		osInfo:   osInfo,
		execType: execType,
		interval: time.Duration(intervalSecs) * time.Second,
		h:        h,
		seen:     make(map[string]struct{}),
		strategy: strategy,
		closer:   closer,
	}
}

func selectStrategy(osInfo detector.OSInfo, execType detector.ExecutorType) (discoveryStrategy, io.Closer) {
	switch execType {
	case detector.Shell, detector.SSH:
		if osInfo.OS == "linux" {
			return &processDiscoveryStrategy{mode: "shell-proc-linux", lister: listLinuxProcessJobs}, nil
		}
		if osInfo.OS == "windows" {
			return &processDiscoveryStrategy{mode: "shell-proc-windows", lister: listWindowsProcessJobs}, nil
		}
		return &directoryDiscoveryStrategy{mode: "shell-dir-fallback", dirs: filterExisting(shellBuildDirs(osInfo.OS))}, nil
	case detector.Docker:
		dockerStrategy, err := newDockerStrategy(osInfo)
		if err != nil {
			log.Warn().Err(err).Msg("Docker API strategy unavailable, falling back to directory monitor")
			return &directoryDiscoveryStrategy{mode: "docker-dir-fallback", dirs: filterExisting(dockerBuildDirs())}, nil
		}
		return dockerStrategy.discoveryStrategy, dockerStrategy.closer
	case detector.Kubernetes:
		return &directoryDiscoveryStrategy{mode: "kubernetes-dir", dirs: filterExisting(kubernetesBuildDirs())}, nil
	default:
		dirs := append(shellBuildDirs(osInfo.OS), dockerBuildDirs()...)
		return &directoryDiscoveryStrategy{mode: "generic-dir", dirs: filterExisting(dirs)}, nil
	}
}

// Start begins the monitoring loop and blocks until the process receives
// SIGINT/SIGTERM or an unrecoverable error occurs.
func (m *Monitor) Start() error {
	ctx, cancel := notifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if m.closer != nil {
		defer func() {
			if err := m.closer.Close(); err != nil {
				log.Debug().Err(err).Msg("Failed to close strategy resources")
			}
		}()
	}

	if m.strategy == nil {
		log.Warn().Msg("No monitor sources available for detected executor/OS")
		return nil
	}

	log.Info().
		Str("mode", m.strategy.Mode()).
		Str("os", m.osInfo.OS).
		Str("executor", string(m.execType)).
		Msg("Starting polling loop")

	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	return m.startLoop(ctx, ticker.C)
}

func (m *Monitor) startLoop(ctx context.Context, tick <-chan time.Time) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick:
			m.poll(ctx)
		}
	}
}

func (m *Monitor) poll(ctx context.Context) {
	jobs, err := m.strategy.Discover(ctx)
	if err != nil {
		log.Debug().Err(err).Str("mode", m.strategy.Mode()).Msg("Cannot discover jobs")
		return
	}

	for _, job := range jobs {
		if _, already := m.seen[job.Identity]; already {
			continue
		}
		m.seen[job.Identity] = struct{}{}

		if job.IsDirectory {
			log.Info().Str("job_dir", job.SourceDir).Str("mode", job.DiscoveryMode).Msg("New job directory detected")
			if err := m.h.HarvestJob(job.SourceDir); err != nil {
				log.Error().Err(err).Str("job_dir", job.SourceDir).Msg("Harvest failed")
			}
			continue
		}

		log.Info().Str("job_id", job.JobID).Str("cmdline", job.Cmdline).Str("mode", job.DiscoveryMode).Msg("New job execution detected")
		env := job.Env
		if env == nil {
			env = map[string]string{}
		}
		env["GL_HARVEST_DISCOVERY_MODE"] = job.DiscoveryMode
		if err := m.h.HarvestProcess(job.JobID, env, job.Cmdline); err != nil {
			log.Error().Err(err).Str("job_id", job.JobID).Msg("Harvest failed")
		}
	}
}

func shellBuildDirs(goos string) []string {
	if goos == "windows" {
		return []string{`C:\GitLab-Runner\builds`}
	}
	dirs := []string{
		"/var/lib/gitlab-runner/builds",
		"/home/gitlab-runner/builds",
	}
	// Glob for any user home builds directories.
	matches, _ := filepath.Glob("/home/*/builds")
	dirs = append(dirs, matches...)
	return dirs
}

func dockerBuildDirs() []string {
	return []string{
		"/builds",
		"/var/lib/gitlab-runner/builds",
	}
}

func kubernetesBuildDirs() []string {
	dirs := []string{"/builds"}
	if proj := os.Getenv("CI_PROJECT_DIR"); proj != "" {
		parent := filepath.Dir(proj)
		if !contains(dirs, parent) {
			dirs = append(dirs, parent)
		}
	}
	return dirs
}

func filterExisting(dirs []string) []string {
	var out []string
	for _, d := range dirs {
		if runtime.GOOS == "windows" {
			d = strings.ReplaceAll(d, "/", `\`)
		}
		if _, err := os.Stat(d); err == nil {
			out = append(out, d)
		}
	}
	return out
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func listLinuxProcessJobs() ([]processJob, error) {
	procEntries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}

	jobs := make([]processJob, 0)
	for _, entry := range procEntries {
		if !entry.IsDir() {
			continue
		}

		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}

		env, err := readProcEnviron(pid)
		if err != nil {
			continue
		}

		jobID := strings.TrimSpace(env["CI_JOB_ID"])
		if jobID == "" {
			continue
		}

		cmdline, err := readProcCmdline(pid)
		if err != nil {
			cmdline = ""
		}

		jobs = append(jobs, processJob{
			PID:     pid,
			JobID:   jobID,
			Cmdline: cmdline,
			Env:     env,
		})
	}

	return jobs, nil
}

func readProcEnviron(pid int) (map[string]string, error) {
	path := filepath.Join("/proc", strconv.Itoa(pid), "environ")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	env := make(map[string]string)
	parts := bytes.Split(data, []byte{0})
	for _, raw := range parts {
		if len(raw) == 0 {
			continue
		}
		kv := string(raw)
		key, value, found := strings.Cut(kv, "=")
		if !found || key == "" {
			continue
		}
		env[key] = value
	}
	return env, nil
}

func readProcCmdline(pid int) (string, error) {
	path := filepath.Join("/proc", strconv.Itoa(pid), "cmdline")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(data) == 0 {
		return "", nil
	}

	parts := bytes.Split(data, []byte{0})
	args := make([]string, 0, len(parts))
	for _, p := range parts {
		if len(p) == 0 {
			continue
		}
		args = append(args, string(p))
	}
	if len(args) == 0 {
		return "", nil
	}

	return strings.Join(args, " "), nil
}
