package harvester

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/docker/docker/api/types/registry"
)

func TestEncodeAuth(t *testing.T) {
	auth := registry.AuthConfig{
		Username:      "gitlab-ci-token",
		Password:      "job-token",
		ServerAddress: "registry.example.com",
	}

	encoded, err := encodeAuth(auth)
	if err != nil {
		t.Fatalf("encodeAuth: %v", err)
	}

	raw, err := base64.URLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("DecodeString: %v", err)
	}

	var got registry.AuthConfig
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Username != auth.Username || got.Password != auth.Password || got.ServerAddress != auth.ServerAddress {
		t.Fatalf("unexpected decoded auth config: %+v", got)
	}
}

func TestImageArchiveName(t *testing.T) {
	tests := []struct {
		name     string
		imageRef string
		want     string
	}{
		{name: "simple tag", imageRef: "registry.example.com/group/app:latest", want: "latest.tar"},
		{name: "normalize unsafe characters", imageRef: "registry.example.com/group/app:release 2026", want: "release_2026.tar"},
		{name: "trim separator only names", imageRef: "registry.example.com/group/app:---", want: "image.tar"},
		{name: "missing tag", imageRef: "registry.example.com/group/app", want: "image.tar"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := imageArchiveName(tt.imageRef); got != tt.want {
				t.Fatalf("imageArchiveName(%q) = %q, want %q", tt.imageRef, got, tt.want)
			}
		})
	}
}

func TestImageTag(t *testing.T) {
	tests := []struct {
		name     string
		imageRef string
		want     string
	}{
		{name: "tag after slash", imageRef: "registry.example.com/group/app:release-1", want: "release-1"},
		{name: "port does not count as tag", imageRef: "registry.example.com:5000/group/app", want: ""},
		{name: "digest only", imageRef: "registry.example.com/group/app@sha256:abc123", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := imageTag(tt.imageRef); got != tt.want {
				t.Fatalf("imageTag(%q) = %q, want %q", tt.imageRef, got, tt.want)
			}
		})
	}
}
