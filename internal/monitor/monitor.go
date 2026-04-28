package monitor

import (
	"bytes"
	"context"
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
	listProc processJobLister
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

var notifyContext = signal.NotifyContext

// New creates a new Monitor instance.
func New(osInfo detector.OSInfo, execType detector.ExecutorType, intervalSecs int, h jobHarvester) *Monitor {
	var lister processJobLister
	if osInfo.OS == "linux" && (execType == detector.Shell || execType == detector.SSH) {
		lister = listLinuxProcessJobs
	} else if osInfo.OS == "windows" && (execType == detector.Shell || execType == detector.SSH) {
		lister = listWindowsProcessJobs
	}

	return &Monitor{
		osInfo:   osInfo,
		execType: execType,
		interval: time.Duration(intervalSecs) * time.Second,
		h:        h,
		seen:     make(map[string]struct{}),
		listProc: lister,
	}
}

// Start begins the monitoring loop and blocks until the process receives
// SIGINT/SIGTERM or an unrecoverable error occurs.
func (m *Monitor) Start() error {
	ctx, cancel := notifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	watchDirs := m.buildDirs()
	if len(watchDirs) == 0 && m.listProc == nil {
		log.Warn().Msg("No monitor sources available for detected executor/OS")
	}

	if m.listProc != nil {
		log.Info().Str("mode", "shell-proc").Str("os", m.osInfo.OS).Msg("Starting polling loop")
	} else {
		log.Info().Strs("watch_dirs", watchDirs).Msg("Starting polling loop")
	}
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	return m.startLoop(ctx, ticker.C, watchDirs)
}

func (m *Monitor) startLoop(ctx context.Context, tick <-chan time.Time, watchDirs []string) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick:
			if m.listProc != nil {
				m.pollProcesses()
			} else {
				m.poll(watchDirs)
			}
		}
	}
}

func (m *Monitor) pollProcesses() {
	jobs, err := m.listProc()
	if err != nil {
		log.Debug().Err(err).Str("os", m.osInfo.OS).Msg("Cannot list process jobs")
		return
	}

	for _, job := range jobs {
		if _, already := m.seen[job.JobID]; already {
			continue
		}
		m.seen[job.JobID] = struct{}{}
		log.Info().Str("job_id", job.JobID).Int("pid", job.PID).Str("cmdline", job.Cmdline).Msg("New job process detected")
		if err := m.h.HarvestProcess(job.JobID, job.Env, job.Cmdline); err != nil {
			log.Error().Err(err).Str("job_id", job.JobID).Int("pid", job.PID).Msg("Harvest failed")
		}
	}
}

func (m *Monitor) poll(dirs []string) {
	for _, dir := range dirs {
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
			if _, already := m.seen[jobDir]; already {
				continue
			}
			m.seen[jobDir] = struct{}{}
			log.Info().Str("job_dir", jobDir).Msg("New job directory detected")
			if err := m.h.HarvestJob(jobDir); err != nil {
				log.Error().Err(err).Str("job_dir", jobDir).Msg("Harvest failed")
			}
		}
	}
}

// buildDirs returns the list of directories to watch based on executor type and OS.
func (m *Monitor) buildDirs() []string {
	var dirs []string

	switch m.execType {
	case detector.Shell, detector.SSH:
		dirs = append(dirs, shellBuildDirs(m.osInfo.OS)...)
	case detector.Docker:
		dirs = append(dirs, dockerBuildDirs()...)
	case detector.Kubernetes:
		dirs = append(dirs, kubernetesBuildDirs()...)
	default:
		// Try all known locations.
		dirs = append(dirs, shellBuildDirs(m.osInfo.OS)...)
		dirs = append(dirs, dockerBuildDirs()...)
	}

	return filterExisting(dirs)
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
