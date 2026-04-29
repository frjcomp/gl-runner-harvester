package gitlab

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/praetorian-inc/titus/pkg/types"
)

func TestRules(t *testing.T) {
	rules := Rules()
	if len(rules) != 3 {
		t.Fatalf("expected 3 rules, got %d", len(rules))
	}
	for _, r := range rules {
		if r.ID == "" || r.Pattern == "" || r.StructuralID == "" {
			t.Fatalf("rule has missing fields: %+v", r)
		}
	}
}

func TestRulesPatternMatching(t *testing.T) {
	rules := Rules()
	patterns := map[string]*regexp.Regexp{}
	for _, rule := range rules {
		re, err := regexp.Compile(rule.Pattern)
		if err != nil {
			t.Fatalf("failed to compile rule %s: %v", rule.ID, err)
		}
		patterns[rule.ID] = re
	}

	tests := []struct {
		name  string
		rule  string
		token string
		match bool
	}{
		{name: "PAT plain token", rule: customPATRuleID, token: "glpat-abcdefghijklmnopqrstuvwxyz", match: true},
		{name: "PAT dotted token", rule: customPATRuleID, token: "glpat-abcdefghij.01.1706s7abu", match: true},
		{name: "PAT too short", rule: customPATRuleID, token: "glpat-short", match: false},
		{name: "RT plain token", rule: customRTRuleID, token: "glrt-abcdefghijklmnopqrstuvwxyz", match: true},
		{name: "RT dotted token", rule: customRTRuleID, token: "glrt-abcdefghij.01.1706s7abu", match: true},
		{name: "RT bad prefix", rule: customRTRuleID, token: "bad-abcdefghij.01.1706s7abu", match: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			re, ok := patterns[tc.rule]
			if !ok {
				t.Fatalf("missing pattern for rule %s", tc.rule)
			}
			got := re.MatchString(tc.token)
			if got != tc.match {
				t.Fatalf("rule %s token %q match=%v want %v", tc.rule, tc.token, got, tc.match)
			}
		})
	}
}

func TestCanValidate(t *testing.T) {
	if !CanValidate(customPATRuleID) || !CanValidate(customCBTRuleID) || !CanValidate(customRTRuleID) {
		t.Fatalf("expected known rule IDs to be validatable")
	}
	if !CanValidate("gitlab.personal_access_token") {
		t.Fatalf("expected PAT alias to be validatable")
	}
	if !CanValidate("foo.gitlab.job-token") {
		t.Fatalf("expected CBT alias to be validatable")
	}
	if !CanValidate("gitlab.runner_token") {
		t.Fatalf("expected RT alias to be validatable")
	}
	if CanValidate("other") {
		t.Fatalf("expected unknown rule ID to be false")
	}
}

func TestConfiguredURLs(t *testing.T) {
	if got := ConfiguredURLs(""); len(got) != 1 || got[0] != defaultURL {
		t.Fatalf("unexpected default urls: %v", got)
	}
	got := ConfiguredURLs("https://gitlab.internal")
	if len(got) != 2 {
		t.Fatalf("expected default+custom urls, got %v", got)
	}
}

func TestTokenValidatorNameAndCanValidate(t *testing.T) {
	v := &TokenValidator{}
	if v.Name() != "gitlab-token-validator" {
		t.Fatalf("unexpected name: %s", v.Name())
	}
	if !v.CanValidate(customPATRuleID) {
		t.Fatalf("expected CanValidate true")
	}
}

func TestExtractToken(t *testing.T) {
	m := &types.Match{NamedGroups: map[string][]byte{"secret": []byte("abc")}}
	if got := extractToken(m); got != "abc" {
		t.Fatalf("expected named group token, got %q", got)
	}

	m = &types.Match{Snippet: types.Snippet{Matching: []byte("xyz")}}
	if got := extractToken(m); got != "xyz" {
		t.Fatalf("expected snippet token, got %q", got)
	}
}

func TestAPIEndpoint(t *testing.T) {
	got, err := apiEndpoint("https://gitlab.example.com/base", "/api/v4/user")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "https://gitlab.example.com/base/api/v4/user" {
		t.Fatalf("unexpected endpoint: %s", got)
	}
}

func TestTokenSpecForRuleID(t *testing.T) {
	if _, ok := tokenSpecForRuleID(customPATRuleID); !ok {
		t.Fatalf("expected PAT spec")
	}
	if _, ok := tokenSpecForRuleID(customCBTRuleID); !ok {
		t.Fatalf("expected CBT spec")
	}
	if spec, ok := tokenSpecForRuleID(customRTRuleID); !ok || spec.method != http.MethodPost {
		t.Fatalf("expected RT POST spec")
	}
	if _, ok := tokenSpecForRuleID("x"); ok {
		t.Fatalf("expected unknown spec=false")
	}
}

func TestRequestBody(t *testing.T) {
	if body, ctype := requestBody(tokenSpec{}, "tok"); body != nil || ctype != "" {
		t.Fatalf("expected nil body for empty builder")
	}

	spec, _ := tokenSpecForRuleID(customRTRuleID)
	body, ctype := requestBody(spec, "glrt-abc")
	if body == nil || ctype != formContentType {
		t.Fatalf("expected form body for runner token")
	}
	b, _ := io.ReadAll(body)
	if !strings.Contains(string(b), "token=glrt-abc") {
		t.Fatalf("unexpected form payload: %s", string(b))
	}
}

func TestValidateAgainstInstanceTokenSpecificEndpoints(t *testing.T) {
	tests := []struct {
		name         string
		ruleID       string
		token        string
		wantPath     string
		wantMethod   string
		wantHeader   string
		wantBodyPart string
	}{
		{name: "PAT", ruleID: customPATRuleID, token: "glpat-abcdefghijklmnopqrstuvwxyz", wantPath: gitLabUserAPIPath, wantMethod: http.MethodGet, wantHeader: "PRIVATE-TOKEN"},
		{name: "PAT dotted", ruleID: customPATRuleID, token: "glpat-abcdefghij.01.1706s7abu", wantPath: gitLabUserAPIPath, wantMethod: http.MethodGet, wantHeader: "PRIVATE-TOKEN"},
		{name: "CBT", ruleID: customCBTRuleID, token: "glcbt-abcdefghijklmnopqrstuvwxyz", wantPath: gitLabJobAPIPath, wantMethod: http.MethodGet, wantHeader: "JOB-TOKEN"},
		{name: "RT", ruleID: customRTRuleID, token: "glrt-abcdefghijklmnopqrstuvwxyz", wantPath: gitLabRunnerVerifyAPIPath, wantMethod: http.MethodPost, wantBodyPart: "token=glrt-abcdefghijklmnopqrstuvwxyz"},
		{name: "RT dotted", ruleID: customRTRuleID, token: "glrt-abcdefghij.01.1706s7abu", wantPath: gitLabRunnerVerifyAPIPath, wantMethod: http.MethodPost, wantBodyPart: "token=glrt-abcdefghij.01.1706s7abu"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tc.wantPath {
					t.Fatalf("expected path %s, got %s", tc.wantPath, r.URL.Path)
				}
				if r.Method != tc.wantMethod {
					t.Fatalf("expected method %s, got %s", tc.wantMethod, r.Method)
				}
				if tc.wantHeader != "" {
					if got := r.Header.Get(tc.wantHeader); got == "" {
						t.Fatalf("expected header %s", tc.wantHeader)
					}
				}
				if tc.wantBodyPart != "" {
					b, _ := io.ReadAll(r.Body)
					if !strings.Contains(string(b), tc.wantBodyPart) {
						t.Fatalf("unexpected body: %s", string(b))
					}
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()

			m := &types.Match{
				RuleID:      tc.ruleID,
				NamedGroups: map[string][]byte{"secret": []byte(tc.token)},
				Snippet:     types.Snippet{Matching: []byte(tc.token)},
			}
			v := &TokenValidator{gitlabURLs: []string{srv.URL}, client: srv.Client()}
			res, err := v.Validate(context.Background(), m)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if res.Status != types.StatusValid {
				t.Fatalf("expected valid status, got %s (%s)", res.Status, res.Message)
			}
		})
	}
}

func TestValidateRejectsBadPrefix(t *testing.T) {
	v := &TokenValidator{gitlabURLs: []string{"https://gitlab.com"}, client: http.DefaultClient}
	m := &types.Match{RuleID: customPATRuleID, NamedGroups: map[string][]byte{"secret": []byte("not-pat")}}

	res, err := v.Validate(context.Background(), m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Status != types.StatusInvalid {
		t.Fatalf("expected invalid status, got %s", res.Status)
	}
}
