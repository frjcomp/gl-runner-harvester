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
	if f.VerificationStatus != "" || f.VerificationMsg != "" {
		t.Fatalf("expected finding unchanged on error")
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
