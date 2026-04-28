package harvester

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/frjcomp/gl-runner-harvester/internal/scanner"
	"github.com/rs/zerolog/log"
)

// Harvester collects source code and environment variables from CI jobs.
type Harvester struct {
	outputDir    string
	scanSecrets  bool
	gitlabURL    string
	harvestFiles bool
}

// New creates a new Harvester.
func New(outputDir string, scanSecrets bool, gitlabURL string, harvestFiles bool) *Harvester {
	return &Harvester{outputDir: outputDir, scanSecrets: scanSecrets, gitlabURL: gitlabURL, harvestFiles: harvestFiles}
}

// JobData holds all harvested information for a single CI job.
type JobData struct {
	JobID        string            `json:"job_id"`
	Timestamp    time.Time         `json:"timestamp"`
	Discovery    string            `json:"discovery"`
	SourceDir    string            `json:"source_dir"`
	EnvVars      map[string]string `json:"env_vars"`
	CIVars       map[string]string `json:"ci_vars"`
	ScanFindings []scanner.Finding `json:"scan_findings,omitempty"`
}

// HarvestJob harvests a specific job build directory.
func (h *Harvester) HarvestJob(jobDir string) error {
	jobID := deriveJobID(jobDir)
	env := collectEnvVars()
	env["GL_HARVEST_DISCOVERY_MODE"] = "directory"
	return h.harvest(jobID, jobDir, env)
}

// HarvestProcess harvests a job discovered from a host process snapshot.
func (h *Harvester) HarvestProcess(jobID string, env map[string]string, cmdline string) error {
	_ = cmdline
	if jobID == "" {
		return fmt.Errorf("job id is required")
	}
	if env == nil {
		env = map[string]string{}
	}
	if strings.TrimSpace(env["GL_HARVEST_DISCOVERY_MODE"]) == "" {
		env["GL_HARVEST_DISCOVERY_MODE"] = "process"
	}
	sourceDir := strings.TrimSpace(env["CI_PROJECT_DIR"])
	return h.harvest(jobID, sourceDir, env)
}

func (h *Harvester) harvest(jobID, sourceDir string, envVars map[string]string) error {
	if !h.harvestFiles {
		if h.scanSecrets {
			findings, err := scanner.Scan(envVars, sourceDir, h.gitlabURL)
			if err != nil {
				log.Warn().Err(err).Str("job_id", jobID).Msg("Secret scan failed")
			} else {
				log.Info().Str("job_id", jobID).Int("findings", len(findings)).Msg("Secret scan complete (scan-only mode)")
			}
		}
		log.Info().Str("job_id", jobID).Msg("Harvest complete (scan-only mode; no files written)")
		return nil
	}

	ts := time.Now()
	destRoot := filepath.Join(h.outputDir, fmt.Sprintf("%s_%s", jobID, ts.Format("20060102_150405")))

	if err := os.MkdirAll(destRoot, 0700); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	data := JobData{
		JobID:     jobID,
		Timestamp: ts,
		Discovery: strings.TrimSpace(envVars["GL_HARVEST_DISCOVERY_MODE"]),
		SourceDir: sourceDir,
		EnvVars:   envVars,
		CIVars:    collectCIVarsFromMap(envVars),
	}

	// Copy source code.
	if sourceDir != "" {
		srcDest := filepath.Join(destRoot, "source")
		if err := copyDir(sourceDir, srcDest); err != nil {
			log.Warn().Err(err).Str("source", sourceDir).Msg("Could not copy source directory")
		} else {
			log.Info().Str("dest", srcDest).Msg("Source code copied")
		}
	}

	// Secret scanning.
	if h.scanSecrets {
		findings, err := scanner.Scan(data.EnvVars, destRoot, h.gitlabURL)
		if err != nil {
			log.Warn().Err(err).Msg("Secret scan failed")
		} else {
			data.ScanFindings = findings
			log.Info().Int("findings", len(findings)).Msg("Secret scan complete")
		}
	}

	// Persist full env snapshots for each detected job run after scanning to
	// avoid re-detecting env values from generated output files.
	if err := writeEnvSnapshots(destRoot, data.EnvVars, data.CIVars); err != nil {
		log.Warn().Err(err).Msg("Failed to write environment snapshots")
	}

	// Write summary JSON.
	summaryPath := filepath.Join(destRoot, "summary.json")
	if err := writeSummary(summaryPath, data); err != nil {
		log.Error().Err(err).Msg("Failed to write summary")
	}

	log.Info().Str("output", destRoot).Str("job_id", jobID).Msg("Harvest complete")
	return nil
}

func deriveJobID(jobDir string) string {
	return filepath.Base(jobDir)
}

func collectEnvVars() map[string]string {
	m := make(map[string]string)
	for _, e := range os.Environ() {
		k, v, _ := strings.Cut(e, "=")
		m[k] = v
	}
	return m
}

func collectCIVars() map[string]string {
	return collectCIVarsFromMap(collectEnvVars())
}

func collectCIVarsFromMap(source map[string]string) map[string]string {
	m := make(map[string]string)
	for k, v := range source {
		if strings.HasPrefix(k, "CI_") || strings.HasPrefix(k, "GITLAB_") {
			m[k] = v
		}
	}
	return m
}

func writeEnvSnapshots(destRoot string, envVars, ciVars map[string]string) error {
	if err := writeMapJSON(filepath.Join(destRoot, "env_vars.json"), envVars); err != nil {
		return err
	}
	return writeMapJSON(filepath.Join(destRoot, "ci_vars.json"), ciVars)
}

func writeMapJSON(path string, data map[string]string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(data)
}

func writeSummary(path string, data JobData) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(data)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return err
	}

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip unreadable paths
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		return copyFile(path, target)
	})
}
