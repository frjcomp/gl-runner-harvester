package monitor

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/frjcomp/gl-runner-harvester/internal/detector"
	"github.com/frjcomp/gl-runner-harvester/internal/harvester"
	"github.com/rs/zerolog/log"
)

// Monitor watches for active GitLab CI/CD jobs and triggers the harvester.
type Monitor struct {
	osInfo   detector.OSInfo
	execType detector.ExecutorType
	interval time.Duration
	h        *harvester.Harvester
	seen     map[string]struct{}
}

// New creates a new Monitor instance.
func New(osInfo detector.OSInfo, execType detector.ExecutorType, intervalSecs int, h *harvester.Harvester) *Monitor {
	return &Monitor{
		osInfo:   osInfo,
		execType: execType,
		interval: time.Duration(intervalSecs) * time.Second,
		h:        h,
		seen:     make(map[string]struct{}),
	}
}

// Start begins the monitoring loop and blocks until the process receives
// SIGINT/SIGTERM or an unrecoverable error occurs.
func (m *Monitor) Start() error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// If we are already inside a CI job (env vars present), harvest immediately.
	if jobID := os.Getenv("CI_JOB_ID"); jobID != "" {
		log.Info().Str("job_id", jobID).Msg("Already inside a CI job; harvesting current environment")
		if err := m.h.HarvestCurrentEnv(jobID); err != nil {
			log.Error().Err(err).Msg("Failed to harvest current environment")
		}
	}

	watchDirs := m.buildDirs()
	if len(watchDirs) == 0 {
		log.Warn().Msg("No build directories to watch; will only check for env-var based jobs")
	}

	log.Info().Strs("watch_dirs", watchDirs).Msg("Starting polling loop")
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			m.poll(watchDirs)
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
