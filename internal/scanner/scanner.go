package scanner

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/frjcomp/gl-runner-harvester/internal/scanner/gitlab"
	"github.com/praetorian-inc/titus/pkg/scanner"
	"github.com/praetorian-inc/titus/pkg/types"
	"github.com/praetorian-inc/titus/pkg/validator"
	"github.com/rs/zerolog/log"
)

// Finding represents a discovered secret or sensitive value.
type Finding struct {
	Type               string `json:"type"`
	Location           string `json:"location"`
	Match              string `json:"match"`
	VerificationStatus string `json:"verification_status,omitempty"`
	VerificationMsg    string `json:"verification_message,omitempty"`
}

var titusCore *scanner.Core

type validationEngine interface {
	CanValidate(ruleID string) bool
	ValidateMatch(ctx context.Context, match *types.Match) (*types.ValidationResult, error)
}

func init() {
	// Initialize Titus scanner with built-in rules and embedded custom rules.
	rules, err := scanner.GetBuiltinRules()
	if err != nil {
		log.Error().Err(err).Msg("failed to get builtin titus rules")
		return
	}
	rules = append(rules, gitlab.Rules()...)

	titusCore, err = scanner.NewCoreWithRules(rules, scanner.NoopLogger{}, nil)
	if err != nil {
		log.Error().Err(err).Msg("failed to create titus core")
		return
	}

	// Enable validator-aware dedupe for rules we can validate.
	titusCore.SetCanValidate(func(ruleID string) bool {
		return gitlab.CanValidate(ruleID)
	})

	log.Info().Int("rules", len(rules)).Msg("Initialized Titus scanner with builtin and embedded custom rules")
}

// Scan scans the given environment variables and files under scanDir for secrets
// using the Titus Go library.
func Scan(envVars map[string]string, scanDir, gitlabURL string) ([]Finding, error) {
	if titusCore == nil {
		return nil, fmt.Errorf("titus scanner not initialized")
	}

	gitlabURLs := gitlab.ConfiguredURLs(gitlabURL)
	validationEngine := validator.NewEngine(4, gitlab.NewValidator(gitlabURLs))

	var findings []Finding

	// Scan environment variables
	for k, v := range envVars {
		source := "env:" + k
		envFindings, err := scanSource(k+"="+v, source, func(_ int) string { return source }, validationEngine)
		if err != nil {
			log.Debug().Err(err).Str("env_var", k).Msg("error scanning environment variable")
			continue
		}
		findings = append(findings, envFindings...)
	}

	// Scan directory
	if scanDir != "" {
		_ = filepath.WalkDir(scanDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d == nil || d.IsDir() {
				return nil
			}

			info, err := d.Info()
			if err != nil || info.Size() > 10*1024*1024 {
				return nil
			}

			content, err := os.ReadFile(path)
			if err != nil {
				return nil
			}

			fileFindings, err := scanSource(string(content), path, func(line int) string {
				return fmt.Sprintf("%s:%d", path, line)
			}, validationEngine)
			if err != nil {
				log.Debug().Err(err).Str("file", path).Msg("error scanning file")
				return nil
			}
			findings = append(findings, fileFindings...)

			return nil
		})
	}

	return findings, nil
}

func scanSource(content, source string, locationFn func(line int) string, validationEngine validationEngine) ([]Finding, error) {
	result, err := titusCore.Scan(content, source)
	if err != nil {
		return nil, err
	}

	findings := make([]Finding, 0, len(result.Matches))
	for _, match := range result.Matches {
		finding := buildFinding(match.RuleName, locationFn(match.Location.Source.Start.Line), string(match.Snippet.Matching))
		if match.ValidationResult != nil {
			finding.VerificationStatus = string(match.ValidationResult.Status)
			finding.VerificationMsg = match.ValidationResult.Message
			if finding.VerificationStatus == "" {
				applyVerification(&finding, match, validationEngine)
			}
			if finding.VerificationStatus == "" && finding.VerificationMsg != "" {
				finding.VerificationStatus = string(types.StatusUndetermined)
			}
		} else {
			applyVerification(&finding, match, validationEngine)
		}
		findings = append(findings, finding)
		logFinding(finding)
	}

	return findings, nil
}

func buildFinding(ruleName, location, secret string) Finding {
	return Finding{
		Type:     ruleName,
		Location: location,
		Match:    secret,
	}
}

func applyVerification(finding *Finding, match *types.Match, validationEngine validationEngine) {
	if finding == nil || match == nil || validationEngine == nil {
		return
	}

	if !validationEngine.CanValidate(match.RuleID) {
		return
	}

	result, err := validationEngine.ValidateMatch(context.Background(), match)
	if err != nil {
		log.Debug().Err(err).Str("rule_id", match.RuleID).Msg("token validation failed")
		finding.VerificationStatus = string(types.StatusUndetermined)
		finding.VerificationMsg = err.Error()
		return
	}
	if result == nil {
		finding.VerificationStatus = string(types.StatusUndetermined)
		finding.VerificationMsg = "validator returned no result"
		return
	}

	finding.VerificationStatus = string(result.Status)
	finding.VerificationMsg = result.Message
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
