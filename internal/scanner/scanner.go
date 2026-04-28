package scanner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/praetorian-inc/titus/pkg/scanner"
	"github.com/rs/zerolog/log"
)

// Finding represents a discovered secret or sensitive value.
type Finding struct {
	Type            string `json:"type"`
	Location        string `json:"location"`
	Match           string `json:"match"`
	VerificationStatus string `json:"verification_status,omitempty"`
	VerificationMsg string `json:"verification_message,omitempty"`
}

// CustomGitLabVerifier stores GitLab instance URLs for GLPAT verification
type CustomGitLabVerifier struct {
	gitlabURLs []string
}

var titusCore *scanner.Core
var gitlabVerifier *CustomGitLabVerifier

func init() {
	// Initialize Titus scanner with built-in rules
	rules, err := scanner.GetBuiltinRules()
	if err != nil {
		log.Error().Err(err).Msg("failed to get builtin titus rules")
		return
	}

	titusCore, err = scanner.NewCoreWithRules(rules, &scanner.NoopLogger{}, nil)
	if err != nil {
		log.Error().Err(err).Msg("failed to create titus core")
		return
	}

	// Initialize custom GitLab verifier
	gitlabVerifier = NewCustomGitLabVerifier()

	// Enable verification for rules we can validate
	titusCore.SetCanValidate(func(ruleID string) bool {
		return gitlabVerifier.CanValidate(ruleID)
	})
}

// NewCustomGitLabVerifier creates a new custom GitLab instance verifier
func NewCustomGitLabVerifier() *CustomGitLabVerifier {
	return &CustomGitLabVerifier{
		gitlabURLs: []string{
			"https://gitlab.com",
		},
	}
}

// AddGitLabInstance adds a custom GitLab instance URL for GLPAT verification
func (v *CustomGitLabVerifier) AddGitLabInstance(url string) {
	if url != "" && !contains(v.gitlabURLs, url) {
		v.gitlabURLs = append(v.gitlabURLs, url)
		log.Debug().Str("url", url).Msg("Added GitLab instance for GLPAT verification")
	}
}

// CanValidate checks if a rule can be validated by this verifier
func (v *CustomGitLabVerifier) CanValidate(ruleID string) bool {
	// Support validation for GitLab PAT rules
	return strings.Contains(ruleID, "gitlab") && strings.Contains(ruleID, "pat")
}

// VerifyGitLabPAT attempts to verify a GitLab PAT against known instances
func (v *CustomGitLabVerifier) VerifyGitLabPAT(pat string) (status string, message string) {
	if pat == "" {
		return "unknown", "token is empty"
	}

	// Check if token matches GitLab PAT format
	if !strings.HasPrefix(pat, "glpat-") {
		return "invalid", "token does not match GitLab PAT format"
	}

	// For now, we detect the token format as valid but log which instances we would verify against
	for _, url := range v.gitlabURLs {
		log.Debug().Str("url", url).Str("token_prefix", pat[:20]).Msg("Would verify GLPAT against GitLab instance")
	}

	// Return valid status since we can't actually validate without network access in this context
	return "valid_format", "GitLab PAT format is valid; would verify against: " + strings.Join(v.gitlabURLs, ", ")
}

func contains(list []string, item string) bool {
	for _, v := range list {
		if v == item {
			return true
		}
	}
	return false
}

// Scan scans the given environment variables and files under scanDir for secrets
// using the Titus Go library.
func Scan(envVars map[string]string, scanDir string) ([]Finding, error) {
	if titusCore == nil {
		return nil, fmt.Errorf("titus scanner not initialized")
	}

	var findings []Finding

	// Scan environment variables
	for k, v := range envVars {
		result, err := titusCore.Scan(k+"="+v, "env:"+k)
		if err != nil {
			log.Debug().Err(err).Str("env_var", k).Msg("error scanning environment variable")
			continue
		}

		for _, match := range result.Matches {
			finding := Finding{
				Type:     match.RuleName,
				Location: "env:" + k,
				Match:    string(match.Snippet.Matching),
			}

			// Populate verification status if available
			if match.ValidationResult != nil {
				finding.VerificationStatus = string(match.ValidationResult.Status)
				finding.VerificationMsg = match.ValidationResult.Message
			} else if gitlabVerifier.CanValidate(match.RuleID) {
				// Perform custom verification for GitLab PATs
				status, msg := gitlabVerifier.VerifyGitLabPAT(finding.Match)
				finding.VerificationStatus = status
				finding.VerificationMsg = msg
			}

			findings = append(findings, finding)

			// Log finding with verification status
			logFinding(finding)
		}
	}

	// Scan directory
	if scanDir != "" {
		_ = filepath.Walk(scanDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info == nil || info.IsDir() || info.Size() > 10*1024*1024 {
				return nil
			}

			content, err := os.ReadFile(path)
			if err != nil {
				return nil
			}

			result, err := titusCore.Scan(string(content), path)
			if err != nil {
				log.Debug().Err(err).Str("file", path).Msg("error scanning file")
				return nil
			}

			for _, match := range result.Matches {
				finding := Finding{
					Type:     match.RuleName,
					Location: fmt.Sprintf("%s:%d", path, match.Location.Source.Start.Line),
					Match:    string(match.Snippet.Matching),
				}

				// Populate verification status if available
				if match.ValidationResult != nil {
					finding.VerificationStatus = string(match.ValidationResult.Status)
					finding.VerificationMsg = match.ValidationResult.Message
				} else if gitlabVerifier.CanValidate(match.RuleID) {
					// Perform custom verification for GitLab PATs
					status, msg := gitlabVerifier.VerifyGitLabPAT(finding.Match)
					finding.VerificationStatus = status
					finding.VerificationMsg = msg
				}

				findings = append(findings, finding)

				// Log finding with verification status
				logFinding(finding)
			}

			return nil
		})
	}

	return findings, nil
}

// logFinding logs a finding with its verification status
func logFinding(f Finding) {
	logger := log.Warn().
		Str("type", f.Type).
		Str("location", f.Location).
		Str("secret", f.Match)

	if f.VerificationStatus != "" {
		logger.Str("verification", f.VerificationStatus)
	}
	if f.VerificationMsg != "" {
		logger.Str("verification_msg", f.VerificationMsg)
	}

	logger.Msg("Secret finding")
}
