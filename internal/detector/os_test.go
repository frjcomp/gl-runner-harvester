package detector

import "testing"

func TestDetectOS(t *testing.T) {
	info := DetectOS()
	if info.OS == "" {
		t.Fatalf("expected OS to be set")
	}
	if info.Arch == "" {
		t.Fatalf("expected Arch to be set")
	}
}
