package gitlab

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/praetorian-inc/titus/pkg/types"
	"github.com/praetorian-inc/titus/pkg/validator"
	"github.com/rs/zerolog/log"
)

const customPATRuleID = "custom.gitlab.pat"
const customCBTRuleID = "custom.gitlab.cbt"
const customRTRuleID = "custom.gitlab.rt"
const defaultURL = "https://gitlab.com"

const (
	gitLabUserAPIPath         = "/api/v4/user"
	gitLabJobAPIPath          = "/api/v4/job"
	gitLabRunnerVerifyAPIPath = "/api/v4/runners/verify"
	formContentType           = "application/x-www-form-urlencoded"
)

type tokenSpec struct {
	prefix      string
	tokenType   string
	method      string
	apiPath     string
	headerName  string
	bodyBuilder func(token string) (io.Reader, string)
}

// Rules returns embedded Titus custom rules for GitLab-related scanning.
func Rules() []*types.Rule {
	rules := []*types.Rule{
		{
			ID:          customPATRuleID,
			Name:        "Custom GitLab Personal Access Token",
			Pattern:     `(?P<secret>glpat-[A-Za-z0-9_-]{20,})`,
			Description: "Detects GitLab Personal Access Tokens in glpat- format",
			Categories:  []string{"token", "gitlab", "custom"},
			Keywords:    []string{"glpat-"},
			BaseScore:   80,
		},
		{
			ID:          customCBTRuleID,
			Name:        "Custom GitLab CI Build Token",
			Pattern:     `(?P<secret>glcbt-[A-Za-z0-9._-]{20,})`,
			Description: "Detects GitLab CI build tokens in glcbt- format",
			Categories:  []string{"token", "gitlab", "custom", "ci"},
			Keywords:    []string{"glcbt-"},
			BaseScore:   75,
		},
		{
			ID:          customRTRuleID,
			Name:        "Custom GitLab Runner Token",
			Pattern:     `(?P<secret>glrt-[A-Za-z0-9_-]{20,})`,
			Description: "Detects GitLab runner tokens in glrt- format",
			Categories:  []string{"token", "gitlab", "custom", "runner"},
			Keywords:    []string{"glrt-"},
			BaseScore:   75,
		},
	}
	for _, rule := range rules {
		rule.StructuralID = rule.ComputeStructuralID()
	}

	return rules
}

// CanValidate returns true when this package can verify the specified rule.
func CanValidate(ruleID string) bool {
	_, ok := tokenSpecForRuleID(ruleID)
	return ok
}

// NewValidator returns a native Titus validator for GitLab token families.
func NewValidator(gitlabURLs []string) validator.Validator {
	return &TokenValidator{
		gitlabURLs: gitlabURLs,
		client:     http.DefaultClient,
	}
}

// ConfiguredURLs returns default plus optional custom GitLab URL when provided.
func ConfiguredURLs(gitlabURL string) []string {
	urls := []string{defaultURL}
	if gitlabURL == "" || gitlabURL == defaultURL {
		return urls
	}
	return append(urls, gitlabURL)
}

// TokenValidator validates GitLab tokens against GitLab API endpoints.
type TokenValidator struct {
	gitlabURLs []string
	client     *http.Client
}

// Name returns the validator name.
func (v *TokenValidator) Name() string {
	return "gitlab-token-validator"
}

// CanValidate returns true for supported GitLab rule IDs.
func (v *TokenValidator) CanValidate(ruleID string) bool {
	return CanValidate(ruleID)
}

// Validate checks GitLab tokens against configured GitLab instances.
func (v *TokenValidator) Validate(ctx context.Context, match *types.Match) (*types.ValidationResult, error) {
	if match == nil {
		return types.NewValidationResult(types.StatusUndetermined, 0, "match is nil"), nil
	}

	spec, ok := tokenSpecForRuleID(match.RuleID)
	if !ok {
		return types.NewValidationResult(types.StatusUndetermined, 0, fmt.Sprintf("unsupported rule id %q", match.RuleID)), nil
	}

	token := extractToken(match)
	if token == "" {
		return types.NewValidationResult(types.StatusUndetermined, 0, "token is empty"), nil
	}
	if !strings.HasPrefix(token, spec.prefix) {
		return types.NewValidationResult(types.StatusInvalid, 1.0, fmt.Sprintf("token does not match GitLab %s format", spec.tokenType)), nil
	}

	hadError := false
	for _, baseURL := range v.gitlabURLs {
		status, msg := v.validateAgainstInstance(ctx, baseURL, spec, token)
		if status == types.StatusValid {
			return types.NewValidationResult(types.StatusValid, 1.0, fmt.Sprintf("GitLab %s accepted by %s", spec.tokenType, baseURL)), nil
		}
		if status == types.StatusUndetermined {
			hadError = true
			log.Debug().Str("url", baseURL).Str("type", spec.tokenType).Str("reason", msg).Msg("GitLab token validation undetermined for instance")
		}
	}

	if hadError {
		return types.NewValidationResult(types.StatusUndetermined, 0.5, fmt.Sprintf("GitLab %s validation could not be completed against all configured instances", spec.tokenType)), nil
	}

	return types.NewValidationResult(types.StatusInvalid, 1.0, fmt.Sprintf("GitLab %s rejected by all configured instances", spec.tokenType)), nil
}

func (v *TokenValidator) validateAgainstInstance(ctx context.Context, baseURL string, spec tokenSpec, token string) (types.ValidationStatus, string) {
	endpoint, err := apiEndpoint(baseURL, spec.apiPath)
	if err != nil {
		return types.StatusUndetermined, err.Error()
	}

	body, contentType := requestBody(spec, token)
	req, err := http.NewRequestWithContext(ctx, spec.method, endpoint, body)
	if err != nil {
		return types.StatusUndetermined, err.Error()
	}
	if spec.headerName != "" {
		req.Header.Set(spec.headerName, token)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := v.client.Do(req)
	if err != nil {
		return types.StatusUndetermined, err.Error()
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		return types.StatusValid, "accepted"
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
		return types.StatusInvalid, "rejected"
	default:
		return types.StatusUndetermined, fmt.Sprintf("unexpected status %d", resp.StatusCode)
	}
}

func extractToken(match *types.Match) string {
	if match.NamedGroups != nil {
		if raw, ok := match.NamedGroups["secret"]; ok && len(raw) > 0 {
			return string(raw)
		}
	}
	return string(match.Snippet.Matching)
}

func apiEndpoint(baseURL, apiPath string) (string, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	u.Path = path.Join(u.Path, apiPath)
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func tokenSpecForRuleID(ruleID string) (tokenSpec, bool) {
	normalized := strings.ToLower(strings.TrimSpace(ruleID))

	switch {
	case normalized == customPATRuleID || (strings.Contains(normalized, "gitlab") && strings.Contains(normalized, "pat")) || strings.Contains(normalized, "personal_access_token") || strings.Contains(normalized, "personal-access-token"):
		return tokenSpec{
			prefix:     "glpat-",
			tokenType:  "PAT",
			method:     http.MethodGet,
			apiPath:    gitLabUserAPIPath,
			headerName: "PRIVATE-TOKEN",
		}, true
	case normalized == customCBTRuleID || (strings.Contains(normalized, "gitlab") && (strings.Contains(normalized, "cbt") || strings.Contains(normalized, "job_token") || strings.Contains(normalized, "job-token"))):
		return tokenSpec{
			prefix:     "glcbt-",
			tokenType:  "CBT",
			method:     http.MethodGet,
			apiPath:    gitLabJobAPIPath,
			headerName: "JOB-TOKEN",
		}, true
	case normalized == customRTRuleID || (strings.Contains(normalized, "gitlab") && (strings.Contains(normalized, "rt") || strings.Contains(normalized, "runner_token") || strings.Contains(normalized, "runner-token") || strings.Contains(normalized, "runner_registration"))):
		return tokenSpec{
			prefix:    "glrt-",
			tokenType: "RT",
			method:    http.MethodPost,
			apiPath:   gitLabRunnerVerifyAPIPath,
			bodyBuilder: func(token string) (io.Reader, string) {
				form := url.Values{}
				form.Set("token", token)
				return strings.NewReader(form.Encode()), formContentType
			},
		}, true
	default:
		return tokenSpec{}, false
	}
}

func requestBody(spec tokenSpec, token string) (io.Reader, string) {
	if spec.bodyBuilder == nil {
		return nil, ""
	}
	return spec.bodyBuilder(token)
}
