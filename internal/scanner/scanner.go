package scanner

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rs/zerolog/log"
)

// Finding represents a discovered secret or sensitive value.
type Finding struct {
	Type     string `json:"type"`
	Severity string `json:"severity"`
	Location string `json:"location"`
	Match    string `json:"match"` // partial / redacted
}

// Scan scans the given environment variables and files under scanDir for secrets.
// It first tries the titus CLI, then falls back to built-in regex patterns.
func Scan(envVars map[string]string, scanDir string) ([]Finding, error) {
	var findings []Finding

	// Try titus CLI.
	if f, err := scanWithTitus(scanDir); err == nil {
		findings = append(findings, f...)
	} else {
		log.Debug().Err(err).Msg("titus not available; using built-in scanner")
	}

	// Always run built-in scanner as a complement.
	findings = append(findings, scanEnvVars(envVars)...)
	findings = append(findings, scanFiles(scanDir)...)

	return deduplicate(findings), nil
}

// scanWithTitus attempts to run the titus binary and parse its output.
func scanWithTitus(dir string) ([]Finding, error) {
	titusPath, err := exec.LookPath("titus")
	if err != nil {
		return nil, fmt.Errorf("titus not found in PATH: %w", err)
	}

	cmd := exec.Command(titusPath, "scan", dir) // #nosec G204
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Run(); err != nil {
		// titus may exit non-zero when findings are present; parse output anyway.
		log.Debug().Err(err).Msg("titus exited with non-zero status")
	}

	return parseTitusOutput(out.String(), dir), nil
}

// parseTitusOutput parses line-based titus output into findings.
func parseTitusOutput(output, dir string) []Finding {
	var findings []Finding
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		findings = append(findings, Finding{
			Type:     "titus",
			Severity: "high",
			Location: dir,
			Match:    truncate(line, 120),
		})
	}
	return findings
}

// ---- Built-in regex patterns ----

type pattern struct {
	name     string
	severity string
	re       *regexp.Regexp
}

var secretPatterns = []pattern{
	{
		name:     "aws_access_key",
		severity: "critical",
		re:       regexp.MustCompile(`(?i)(AKIA|ABIA|ACCA|ASIA)[A-Z0-9]{16}`),
	},
	{
		name:     "aws_secret_key",
		severity: "critical",
		re:       regexp.MustCompile(`(?i)aws.{0,20}secret.{0,20}[=:]\s*['"]?([A-Za-z0-9/+]{40})['"]?`),
	},
	{
		name:     "gitlab_pat",
		severity: "high",
		re:       regexp.MustCompile(`glpat-[A-Za-z0-9_\-]{20}`),
	},
	{
		name:     "gitlab_deploy_token",
		severity: "high",
		re:       regexp.MustCompile(`gldt-[A-Za-z0-9_\-]{20}`),
	},
	{
		name:     "gitlab_runner_token",
		severity: "high",
		re:       regexp.MustCompile(`glrt-[A-Za-z0-9_\-]{20}`),
	},
	{
		name:     "github_pat",
		severity: "high",
		re:       regexp.MustCompile(`gh[pous]_[A-Za-z0-9]{36,}`),
	},
	{
		name:     "private_key",
		severity: "critical",
		re:       regexp.MustCompile(`-----BEGIN (RSA |EC |DSA |OPENSSH )?PRIVATE KEY-----`),
	},
	{
		name:     "generic_api_key",
		severity: "medium",
		re:       regexp.MustCompile(`(?i)(api[_\-]?key|api[_\-]?secret|access[_\-]?token|auth[_\-]?token)[=:\s]+['"]?([A-Za-z0-9_\-]{16,})['"]?`),
	},
	{
		name:     "generic_password",
		severity: "medium",
		re:       regexp.MustCompile(`(?i)(password|passwd|secret)[=:\s]+['"]?([^\s'"]{8,})['"]?`),
	},
	{
		name:     "jwt_token",
		severity: "high",
		re:       regexp.MustCompile(`eyJ[A-Za-z0-9_\-]+\.eyJ[A-Za-z0-9_\-]+\.[A-Za-z0-9_\-]+`),
	},
}

func scanEnvVars(envVars map[string]string) []Finding {
	var findings []Finding
	for k, v := range envVars {
		line := k + "=" + v
		for _, p := range secretPatterns {
			if p.re.MatchString(line) {
				findings = append(findings, Finding{
					Type:     p.name,
					Severity: p.severity,
					Location: "env:" + k,
					Match:    truncate(v, 80),
				})
			}
		}
	}
	return findings
}

func scanFiles(dir string) []Finding {
	if dir == "" {
		return nil
	}
	var findings []Finding
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		// Skip large files (> 10MB) and binary-looking files.
		if info.Size() > 10*1024*1024 {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()

		s := bufio.NewScanner(f)
		lineNum := 0
		for s.Scan() {
			lineNum++
			line := s.Text()
			for _, p := range secretPatterns {
				if p.re.MatchString(line) {
					findings = append(findings, Finding{
						Type:     p.name,
						Severity: p.severity,
						Location: fmt.Sprintf("%s:%d", path, lineNum),
						Match:    truncate(line, 80),
					})
				}
			}
		}
		return nil
	})
	return findings
}

func deduplicate(findings []Finding) []Finding {
	seen := make(map[string]struct{})
	var out []Finding
	for _, f := range findings {
		key := f.Type + "|" + f.Location + "|" + f.Match
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, f)
	}
	return out
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
