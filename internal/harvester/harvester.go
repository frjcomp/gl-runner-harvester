package harvester

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/frjcomp/gl-runner-harvester/internal/retriever"
	"github.com/frjcomp/gl-runner-harvester/internal/scanner"
	"github.com/rs/zerolog/log"
)

// Harvester collects source code and environment variables from CI jobs.
type Harvester struct {
	outputDir      string
	scanSecrets    bool
	gitlabURL      string
	harvestFiles   bool
	secureFiles    bool
	harvestImages  bool
	secFilesClient *retriever.SecureFilesRetriever
	registryClient *retriever.RegistryRetriever
}

// Config holds optional configuration for Harvester features.
type Config struct {
	OutputDir     string
	ScanSecrets   bool
	GitLabURL     string
	HarvestFiles  bool
	SecureFiles   bool
	HarvestImages bool
}

// New creates a new Harvester.
func New(cfg Config) *Harvester {
	return &Harvester{
		outputDir:      cfg.OutputDir,
		scanSecrets:    cfg.ScanSecrets,
		gitlabURL:      cfg.GitLabURL,
		harvestFiles:   cfg.HarvestFiles,
		secureFiles:    cfg.SecureFiles,
		harvestImages:  cfg.HarvestImages,
		secFilesClient: retriever.NewSecureFiles(cfg.GitLabURL),
		registryClient: retriever.NewRegistry(cfg.GitLabURL),
	}
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

	// Fetch GitLab secure files.
	if h.secureFiles {
		token := strings.TrimSpace(envVars["CI_JOB_TOKEN"])
		projectID := strings.TrimSpace(envVars["CI_PROJECT_ID"])
		if token != "" && projectID != "" {
			sfDir := filepath.Join(destRoot, "secure_files")
			if err := h.secFilesClient.FetchAll(context.Background(), token, projectID, sfDir); err != nil {
				log.Warn().Err(err).Str("job_id", jobID).Msg("Secure files fetch failed")
			} else {
				log.Info().Str("job_id", jobID).Str("dest", sfDir).Msg("Secure files fetched")
			}
		} else {
			log.Debug().Str("job_id", jobID).Msg("Skipping secure files: CI_JOB_TOKEN or CI_PROJECT_ID not available")
		}
	}

	// Pull and save the project's latest registry image.
	if h.harvestImages {
		token := strings.TrimSpace(envVars["CI_JOB_TOKEN"])
		projectID := strings.TrimSpace(envVars["CI_PROJECT_ID"])
		ciRegistry := strings.TrimSpace(envVars["CI_REGISTRY"])
		if token != "" && projectID != "" && ciRegistry != "" {
			imageRef, err := h.registryClient.LatestImageRef(context.Background(), token, projectID, ciRegistry)
			if err != nil {
				log.Warn().Err(err).Str("job_id", jobID).Msg("Registry image lookup failed")
			} else if imageRef != "" {
				imgDir := filepath.Join(destRoot, "image")
				if err := HarvestImage(context.Background(), envVars, imageRef, imgDir); err != nil {
					log.Warn().Err(err).Str("job_id", jobID).Str("image", imageRef).Msg("Image harvest failed")
				}
			} else {
				log.Info().Str("job_id", jobID).Msg("No registry images found for project")
			}
		} else {
			log.Debug().Str("job_id", jobID).Msg("Skipping image harvest: CI_JOB_TOKEN, CI_PROJECT_ID or CI_REGISTRY not available")
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

func collectCIVarsFromMap(source map[string]string) map[string]string {
	m := make(map[string]string)
	for k, v := range source {
		if strings.HasPrefix(k, "CI_") || strings.HasPrefix(k, "GITLAB_") {
			m[k] = v
		}
	}
	return m
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
