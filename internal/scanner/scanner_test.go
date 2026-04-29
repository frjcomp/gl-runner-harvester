package scanner

import (
	"context"
	"errors"
	"testing"

	"github.com/praetorian-inc/titus/pkg/types"
)

type fakeValidationEngine struct {
	canValidate bool
	result      *types.ValidationResult
	err         error
}

func (f fakeValidationEngine) CanValidate(string) bool { return f.canValidate }

func (f fakeValidationEngine) ValidateMatch(context.Context, *types.Match) (*types.ValidationResult, error) {
	return f.result, f.err
}

func TestBuildFinding(t *testing.T) {
	f := buildFinding("rule", "loc", "secret")
	if f.Type != "rule" || f.Location != "loc" || f.Match != "secret" {
		t.Fatalf("unexpected finding: %+v", f)
	}
}

func TestApplyVerificationGuards(t *testing.T) {
	applyVerification(nil, nil, nil)

	f := &Finding{}
	applyVerification(f, nil, fakeValidationEngine{})
	if f.VerificationStatus != "" || f.VerificationMsg != "" {
		t.Fatalf("expected untouched finding")
	}
}

func TestApplyVerificationNoValidator(t *testing.T) {
	f := &Finding{Match: "x"}
	m := &types.Match{RuleID: "rule"}
	applyVerification(f, m, fakeValidationEngine{canValidate: false})
	if f.VerificationStatus != "" {
		t.Fatalf("expected no status when validator cannot handle rule")
	}
}

func TestApplyVerificationSetsResult(t *testing.T) {
	f := &Finding{Match: "x"}
	m := &types.Match{RuleID: "rule"}
	res := types.NewValidationResult(types.StatusValid, 1.0, "ok")
	applyVerification(f, m, fakeValidationEngine{canValidate: true, result: res})

	if f.VerificationStatus != "valid" {
		t.Fatalf("expected valid status, got %q", f.VerificationStatus)
	}
	if f.VerificationMsg != "ok" {
		t.Fatalf("expected message to be set")
	}
}

func TestApplyVerificationErrorIsIgnored(t *testing.T) {
	f := &Finding{Match: "x"}
	m := &types.Match{RuleID: "rule"}
	applyVerification(f, m, fakeValidationEngine{canValidate: true, err: errors.New("boom")})
	if f.VerificationStatus != "undetermined" {
		t.Fatalf("expected undetermined status on error, got %q", f.VerificationStatus)
	}
	if f.VerificationMsg != "boom" {
		t.Fatalf("expected error message to be propagated")
	}
}

func TestApplyVerificationNilResult(t *testing.T) {
	f := &Finding{Match: "x"}
	m := &types.Match{RuleID: "rule"}
	applyVerification(f, m, fakeValidationEngine{canValidate: true, result: nil})
	if f.VerificationStatus != "undetermined" {
		t.Fatalf("expected undetermined status on nil result, got %q", f.VerificationStatus)
	}
	if f.VerificationMsg != "validator returned no result" {
		t.Fatalf("expected nil result message, got %q", f.VerificationMsg)
	}
}

func TestScanWhenCoreNotInitialized(t *testing.T) {
	old := titusCore
	titusCore = nil
	defer func() { titusCore = old }()

	_, err := Scan(map[string]string{}, "", "")
	if err == nil {
		t.Fatalf("expected initialization error")
	}
}

func TestHasVerification(t *testing.T) {
	tests := []struct {
		name    string
		finding Finding
		want    bool
	}{
		{name: "no verification", finding: Finding{Match: "a"}, want: false},
		{name: "status only", finding: Finding{Match: "a", VerificationStatus: "valid"}, want: true},
		{name: "message only", finding: Finding{Match: "a", VerificationMsg: "ok"}, want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := hasVerification(tc.finding)
			if got != tc.want {
				t.Fatalf("hasVerification() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDedupeFindings(t *testing.T) {
	tests := []struct {
		name     string
		input    []Finding
		want     []Finding
		wantSize int
	}{
		{
			name: "keeps unique matches",
			input: []Finding{
				{Type: "a", Location: "env:A", Match: "secret-1"},
				{Type: "b", Location: "env:B", Match: "secret-2"},
			},
			want: []Finding{
				{Type: "a", Location: "env:A", Match: "secret-1"},
				{Type: "b", Location: "env:B", Match: "secret-2"},
			},
			wantSize: 2,
		},
		{
			name: "dedupes unverified duplicates by first seen",
			input: []Finding{
				{Type: "first", Location: "env:A", Match: "secret-1"},
				{Type: "second", Location: "env:B", Match: "secret-1"},
			},
			want: []Finding{
				{Type: "first", Location: "env:A", Match: "secret-1"},
			},
			wantSize: 1,
		},
		{
			name: "prefers verified duplicate",
			input: []Finding{
				{Type: "plain", Location: "env:URL", Match: "secret-1"},
				{Type: "verified", Location: "env:TOKEN", Match: "secret-1", VerificationStatus: "valid", VerificationMsg: "accepted"},
			},
			want: []Finding{
				{Type: "verified", Location: "env:TOKEN", Match: "secret-1", VerificationStatus: "valid", VerificationMsg: "accepted"},
			},
			wantSize: 1,
		},
		{
			name: "keeps first verified when duplicate is also verified",
			input: []Finding{
				{Type: "first", Location: "env:A", Match: "secret-1", VerificationStatus: "invalid", VerificationMsg: "rejected"},
				{Type: "second", Location: "env:B", Match: "secret-1", VerificationStatus: "valid", VerificationMsg: "accepted"},
			},
			want: []Finding{
				{Type: "first", Location: "env:A", Match: "secret-1", VerificationStatus: "invalid", VerificationMsg: "rejected"},
			},
			wantSize: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := dedupeFindings(tc.input)
			if len(got) != tc.wantSize {
				t.Fatalf("dedupeFindings() length = %d, want %d", len(got), tc.wantSize)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("dedupeFindings()[%d] = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}
