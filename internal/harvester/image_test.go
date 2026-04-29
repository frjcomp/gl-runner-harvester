package harvester

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/registry"
	"github.com/docker/docker/client"
)

type fakeImageDockerClient struct {
	pullErr          error
	saveErr          error
	removeErr        error
	pullCalls        int
	saveCalls        int
	removeCalls      int
	lastPullRef      string
	lastPullAuth     string
	lastSaveRefs     []string
	lastRemoveRef    string
	pullResponse     io.ReadCloser
	saveResponse     io.ReadCloser
	closeCalledCount int
}

func (f *fakeImageDockerClient) ImagePull(_ context.Context, ref string, options image.PullOptions) (io.ReadCloser, error) {
	f.pullCalls++
	f.lastPullRef = ref
	f.lastPullAuth = options.RegistryAuth
	if f.pullErr != nil {
		return nil, f.pullErr
	}
	if f.pullResponse != nil {
		return f.pullResponse, nil
	}
	return io.NopCloser(bytes.NewReader([]byte("pulled"))), nil
}

func (f *fakeImageDockerClient) ImageSave(_ context.Context, refs []string, _ ...client.ImageSaveOption) (io.ReadCloser, error) {
	f.saveCalls++
	f.lastSaveRefs = refs
	if f.saveErr != nil {
		return nil, f.saveErr
	}
	if f.saveResponse != nil {
		return f.saveResponse, nil
	}
	return io.NopCloser(bytes.NewReader([]byte("tar-content"))), nil
}

func (f *fakeImageDockerClient) ImageRemove(_ context.Context, ref string, _ image.RemoveOptions) ([]image.DeleteResponse, error) {
	f.removeCalls++
	f.lastRemoveRef = ref
	if f.removeErr != nil {
		return nil, f.removeErr
	}
	return nil, nil
}

func (f *fakeImageDockerClient) Close() error {
	f.closeCalledCount++
	return nil
}

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

func TestHarvestImageSuccessWithRegistryAuth(t *testing.T) {
	oldFactory := newImageDockerClient
	defer func() { newImageDockerClient = oldFactory }()

	fake := &fakeImageDockerClient{}
	newImageDockerClient = func() (imageDockerClient, error) {
		return fake, nil
	}

	dest := t.TempDir()
	err := HarvestImage(context.Background(), map[string]string{
		"CI_REGISTRY":  "registry.example.com",
		"CI_JOB_TOKEN": "token-123",
	}, "registry.example.com/group/app:latest", dest)
	if err != nil {
		t.Fatalf("HarvestImage: %v", err)
	}

	if fake.pullCalls != 1 || fake.saveCalls != 1 || fake.removeCalls != 1 {
		t.Fatalf("expected pull/save/remove calls (1/1/1), got %d/%d/%d", fake.pullCalls, fake.saveCalls, fake.removeCalls)
	}
	if fake.lastPullAuth == "" {
		t.Fatalf("expected registry auth to be set")
	}

	archive := filepath.Join(dest, "latest.tar")
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatalf("read saved archive: %v", err)
	}
	if string(data) != "tar-content" {
		t.Fatalf("unexpected archive contents: %q", string(data))
	}
}

func TestHarvestImageSuccessWithoutRegistryAuth(t *testing.T) {
	oldFactory := newImageDockerClient
	defer func() { newImageDockerClient = oldFactory }()

	fake := &fakeImageDockerClient{}
	newImageDockerClient = func() (imageDockerClient, error) {
		return fake, nil
	}

	err := HarvestImage(context.Background(), map[string]string{}, "registry.example.com/group/app:latest", t.TempDir())
	if err != nil {
		t.Fatalf("HarvestImage: %v", err)
	}
	if fake.lastPullAuth != "" {
		t.Fatalf("expected empty registry auth when env is missing")
	}
}

func TestHarvestImagePullError(t *testing.T) {
	oldFactory := newImageDockerClient
	defer func() { newImageDockerClient = oldFactory }()

	fake := &fakeImageDockerClient{pullErr: errors.New("pull failed")}
	newImageDockerClient = func() (imageDockerClient, error) {
		return fake, nil
	}

	err := HarvestImage(context.Background(), map[string]string{}, "registry.example.com/group/app:latest", t.TempDir())
	if err == nil {
		t.Fatalf("expected error")
	}
	if isDockerDaemonAccessError(err) {
		t.Fatalf("did not expect daemon access classification for generic pull error")
	}
}

func TestHarvestImageClassifiesDockerDaemonAccessFailureOnPull(t *testing.T) {
	oldFactory := newImageDockerClient
	defer func() { newImageDockerClient = oldFactory }()

	fake := &fakeImageDockerClient{
		pullErr: errors.New("permission denied while trying to connect to the Docker daemon socket at unix:///var/run/docker.sock: dial unix /var/run/docker.sock: connect: permission denied"),
	}
	newImageDockerClient = func() (imageDockerClient, error) {
		return fake, nil
	}

	err := HarvestImage(context.Background(), map[string]string{}, "registry.example.com/group/app:latest", t.TempDir())
	if err == nil {
		t.Fatalf("expected error")
	}
	if !isDockerDaemonAccessError(err) {
		t.Fatalf("expected docker daemon access error on pull, got: %v", err)
	}
}

func TestHarvestImageClassifiesDockerDaemonAccessFailureOnSave(t *testing.T) {
	oldFactory := newImageDockerClient
	defer func() { newImageDockerClient = oldFactory }()

	fake := &fakeImageDockerClient{
		saveErr: errors.New("cannot connect to the docker daemon at unix:///var/run/docker.sock"),
	}
	newImageDockerClient = func() (imageDockerClient, error) {
		return fake, nil
	}

	err := HarvestImage(context.Background(), map[string]string{}, "registry.example.com/group/app:latest", t.TempDir())
	if err == nil {
		t.Fatalf("expected error")
	}
	if !isDockerDaemonAccessError(err) {
		t.Fatalf("expected docker daemon access error on save, got: %v", err)
	}
}

func TestLooksLikeDockerDaemonAccessError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "permission denied docker socket",
			err:  errors.New("permission denied while trying to connect to the Docker daemon socket at unix:///var/run/docker.sock"),
			want: true,
		},
		{
			name: "socket missing",
			err:  errors.New("dial unix /var/run/docker.sock: connect: no such file or directory"),
			want: true,
		},
		{
			name: "generic creation failure",
			err:  errors.New("invalid docker host configuration"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := looksLikeDockerDaemonAccessError(tt.err); got != tt.want {
				t.Fatalf("looksLikeDockerDaemonAccessError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHarvestImageClassifiesDockerDaemonAccessFailure(t *testing.T) {
	oldFactory := newImageDockerClient
	defer func() { newImageDockerClient = oldFactory }()

	newImageDockerClient = func() (imageDockerClient, error) {
		return nil, errors.New("permission denied while trying to connect to the Docker daemon socket at unix:///var/run/docker.sock")
	}

	err := HarvestImage(context.Background(), map[string]string{}, "registry.example.com/group/app:latest", t.TempDir())
	if err == nil {
		t.Fatalf("expected error")
	}
	if !isDockerDaemonAccessError(err) {
		t.Fatalf("expected docker daemon access error, got: %v", err)
	}
}

func TestHarvestImageNonDaemonClientFailureIsNotClassified(t *testing.T) {
	oldFactory := newImageDockerClient
	defer func() { newImageDockerClient = oldFactory }()

	newImageDockerClient = func() (imageDockerClient, error) {
		return nil, errors.New("invalid docker host configuration")
	}

	err := HarvestImage(context.Background(), map[string]string{}, "registry.example.com/group/app:latest", t.TempDir())
	if err == nil {
		t.Fatalf("expected error")
	}
	if isDockerDaemonAccessError(err) {
		t.Fatalf("did not expect docker daemon access classification, got: %v", err)
	}
}
