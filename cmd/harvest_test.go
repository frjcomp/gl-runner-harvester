package cmd

import (
	"strings"
	"testing"

	"github.com/frjcomp/gl-runner-harvester/internal/detector"
)

func TestNormalizeGitLabURL(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "default", input: "https://gitlab.com", want: "https://gitlab.com"},
		{name: "host only", input: "gitlab.example.com", want: "https://gitlab.example.com"},
		{name: "trim path and slash", input: "https://gitlab.example.com/api/", want: "https://gitlab.example.com/api"},
		{name: "strip query and fragment", input: "https://gitlab.example.com/path?q=1#x", want: "https://gitlab.example.com/path"},
		{name: "empty", input: "   ", wantErr: true},
		{name: "invalid", input: "://bad", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeGitLabURL(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestParseManualExecutor(t *testing.T) {
	tests := []struct {
		in      string
		wantErr bool
	}{
		{in: "shell"},
		{in: "SSH"},
		{in: " docker "},
		{in: "kubernetes"},
		{in: "bad", wantErr: true},
	}

	for _, tc := range tests {
		_, err := parseManualExecutor(tc.in)
		if tc.wantErr && err == nil {
			t.Fatalf("input %q expected error", tc.in)
		}
		if !tc.wantErr && err != nil {
			t.Fatalf("input %q unexpected error: %v", tc.in, err)
		}
	}
}

func TestRunHarvestInvalidManualExecutor(t *testing.T) {
	oldExec := executor
	defer func() { executor = oldExec }()

	executor = "invalid"
	err := runHarvest(nil, nil)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "invalid --executor") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPrintDetectionSummaryNoPanic(t *testing.T) {
	printDetectionSummary(
		detector.OSInfo{OS: "linux"},
		detector.Shell,
		detector.PermissionInfo{},
	)
}

func TestValidateMaxDiskUsagePercent(t *testing.T) {
	tests := []struct {
		name    string
		value   float64
		wantErr bool
	}{
		{name: "valid default", value: 95, wantErr: false},
		{name: "valid decimal", value: 80.5, wantErr: false},
		{name: "invalid zero", value: 0, wantErr: true},
		{name: "invalid negative", value: -1, wantErr: true},
		{name: "invalid hundred", value: 100, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateMaxDiskUsagePercent(tc.value)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error for value %.2f", tc.value)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error for value %.2f: %v", tc.value, err)
			}
		})
	}
}

func TestRunHarvestInvalidMaxDiskUsagePercent(t *testing.T) {
	oldValue := maxDiskUsagePct
	defer func() { maxDiskUsagePct = oldValue }()

	maxDiskUsagePct = 100
	err := runHarvest(nil, nil)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "invalid --max-disk-usage-percent") {
		t.Fatalf("unexpected error: %v", err)
	}
}
