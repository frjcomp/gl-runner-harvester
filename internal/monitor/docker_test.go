package monitor

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
)

func TestDockerHostForOSDefaults(t *testing.T) {
	t.Setenv("DOCKER_HOST", "")

	host, err := dockerHostForOS("linux")
	if err != nil {
		t.Fatalf("dockerHostForOS linux: %v", err)
	}
	if host != "unix:///var/run/docker.sock" {
		t.Fatalf("unexpected linux default host: %q", host)
	}

	host, err = dockerHostForOS("windows")
	if err != nil {
		t.Fatalf("dockerHostForOS windows: %v", err)
	}
	if host != "npipe:////./pipe/docker_engine" {
		t.Fatalf("unexpected windows default host: %q", host)
	}
}

func TestDockerHostForOSAcceptsTCPHost(t *testing.T) {
	t.Setenv("DOCKER_HOST", "tcp://127.0.0.1:2375")
	host, err := dockerHostForOS("linux")
	if err != nil {
		t.Fatalf("dockerHostForOS tcp host: %v", err)
	}
	if host != "tcp://127.0.0.1:2375" {
		t.Fatalf("unexpected tcp host: %q", host)
	}
}

func TestDockerSourceDirFallback(t *testing.T) {
	mounts := []container.MountPoint{
		{Destination: "/data", Source: "/tmp/data"},
		{Destination: "/builds/project", Source: "/srv/gitlab/builds/project"},
	}

	got := dockerSourceDirFallback(mounts)
	if got != "/srv/gitlab/builds/project" {
		t.Fatalf("unexpected source dir fallback: %q", got)
	}
}

func TestResolveContainerProjectDirToHost(t *testing.T) {
	mounts := []container.MountPoint{
		{Destination: "/builds", Source: "/host/builds"},
		{Destination: "/cache", Source: "/host/cache"},
	}

	got := resolveContainerProjectDirToHost("/builds/group/project", mounts)
	if got != "/host/builds/group/project" {
		t.Fatalf("unexpected mapped path: %q", got)
	}
}

func TestResolveContainerProjectDirToHostPrefersMostSpecificMount(t *testing.T) {
	mounts := []container.MountPoint{
		{Destination: "/builds", Source: "/host/builds"},
		{Destination: "/builds/group", Source: "/host/group"},
	}

	got := resolveContainerProjectDirToHost("/builds/group/project", mounts)
	if got != "/host/group/project" {
		t.Fatalf("expected most specific mount match, got %q", got)
	}
}

func TestResolveContainerProjectDirToHostNoMatch(t *testing.T) {
	mounts := []container.MountPoint{{Destination: "/cache", Source: "/host/cache"}}
	if got := resolveContainerProjectDirToHost("/builds/group/project", mounts); got != "" {
		t.Fatalf("expected empty mapping when no mount matches, got %q", got)
	}
}

func TestResolveContainerProjectDirToHostNamedVolumeMount(t *testing.T) {
	mounts := []container.MountPoint{{Type: "volume", Destination: "/builds", Source: "/var/lib/docker/volumes/v1/_data"}}
	if got := resolveContainerProjectDirToHost("/builds/group/project", mounts); got != "" {
		t.Fatalf("expected empty mapping for volume mount, got %q", got)
	}
}

func TestShouldExtractFromContainer(t *testing.T) {
	if !shouldExtractFromContainer("", "/builds/group/project") {
		t.Fatalf("expected fallback extraction when sourceDir is empty and project dir exists")
	}
	if shouldExtractFromContainer("/host/builds/group/project", "/builds/group/project") {
		t.Fatalf("did not expect fallback extraction when sourceDir is resolved")
	}
	if shouldExtractFromContainer("", "") {
		t.Fatalf("did not expect fallback extraction when project dir is empty")
	}
}

func TestExtractTarArchive(t *testing.T) {
	tmp := t.TempDir()

	buf := &bytes.Buffer{}
	tw := tar.NewWriter(buf)

	if err := tw.WriteHeader(&tar.Header{Name: "project", Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
		t.Fatalf("write dir header: %v", err)
	}
	content := []byte("hello from archive")
	if err := tw.WriteHeader(&tar.Header{Name: "project/README.md", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(content))}); err != nil {
		t.Fatalf("write file header: %v", err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatalf("write file content: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}

	if err := extractTarArchive(bytes.NewReader(buf.Bytes()), tmp); err != nil {
		t.Fatalf("extractTarArchive: %v", err)
	}

	outFile := filepath.Join(tmp, "project", "README.md")
	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read extracted file: %v", err)
	}
	if string(data) != string(content) {
		t.Fatalf("unexpected extracted file content: %q", string(data))
	}
}

func TestExtractTarArchiveRejectsUnsafePath(t *testing.T) {
	tmp := t.TempDir()

	buf := &bytes.Buffer{}
	tw := tar.NewWriter(buf)
	content := []byte("escape")
	if err := tw.WriteHeader(&tar.Header{Name: "../../etc/passwd", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(content))}); err != nil {
		t.Fatalf("write file header: %v", err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatalf("write file content: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}

	if err := extractTarArchive(bytes.NewReader(buf.Bytes()), tmp); err == nil {
		t.Fatalf("expected extractTarArchive to reject unsafe path")
	}
}

func TestInferExtractedProjectRoot(t *testing.T) {
	tmp := t.TempDir()
	projectRoot := filepath.Join(tmp, "project")
	if err := os.MkdirAll(projectRoot, 0o700); err != nil {
		t.Fatalf("mkdir project root: %v", err)
	}

	if got := inferExtractedProjectRoot(tmp, "project"); got != projectRoot {
		t.Fatalf("expected inferred project root %q, got %q", projectRoot, got)
	}
}

func TestSafeTarPath(t *testing.T) {
	tests := []struct {
		name string
		in   string
		ok   bool
		want string
	}{
		{name: "valid relative", in: "project/src/main.go", ok: true, want: filepath.Join("project", "src", "main.go")},
		{name: "reject traversal", in: "../etc/passwd", ok: false},
		{name: "reject absolute", in: "/etc/passwd", ok: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := safeTarPath(tc.in)
			if ok != tc.ok {
				t.Fatalf("safeTarPath(%q) ok=%v want %v", tc.in, ok, tc.ok)
			}
			if tc.ok && got != tc.want {
				t.Fatalf("safeTarPath(%q)=%q want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestResolveSourceDirWithFallback(t *testing.T) {
	tests := []struct {
		name                string
		containerProjectDir string
		mounts              []container.MountPoint
		extractedPath       string
		extractErr          error
		wantPath            string
		wantCalled          bool
		wantExtracted       bool
		wantErr             bool
	}{
		{
			name:                "uses host mount when resolved",
			containerProjectDir: "/builds/group/project",
			mounts:              []container.MountPoint{{Destination: "/builds", Source: "/host/builds"}},
			wantPath:            filepath.Join("/host/builds", "group", "project"),
			wantCalled:          false,
			wantExtracted:       false,
			wantErr:             false,
		},
		{
			name:                "falls back to extraction for named volume",
			containerProjectDir: "/builds/group/project",
			mounts:              []container.MountPoint{{Type: "volume", Destination: "/builds", Source: "/var/lib/docker/volumes/v1/_data"}},
			extractedPath:       "/tmp/extracted/project",
			wantPath:            "/tmp/extracted/project",
			wantCalled:          true,
			wantExtracted:       true,
			wantErr:             false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			extractor := func(_ context.Context, _ string, _ string) (string, error) {
				called = true
				return tc.extractedPath, tc.extractErr
			}

			got, extracted, err := resolveSourceDirWithFallback(context.Background(), "cid", tc.containerProjectDir, tc.mounts, extractor)

			if (err != nil) != tc.wantErr {
				t.Fatalf("resolveSourceDirWithFallback err=%v wantErr=%v", err, tc.wantErr)
			}
			if got != tc.wantPath {
				t.Fatalf("resolveSourceDirWithFallback path=%q want %q", got, tc.wantPath)
			}
			if called != tc.wantCalled {
				t.Fatalf("extractor called=%v want %v", called, tc.wantCalled)
			}
			if extracted != tc.wantExtracted {
				t.Fatalf("extracted=%v want %v", extracted, tc.wantExtracted)
			}
		})
	}
}

func TestIsLikelyGitLabJobContainer(t *testing.T) {
	if !isLikelyGitLabJobContainer(map[string]string{"CI_JOB_ID": "123"}) {
		t.Fatalf("expected CI_JOB_ID container to be included")
	}
	if !isLikelyGitLabJobContainer(map[string]string{"CI_PROJECT_DIR": "/builds/g/p"}) {
		t.Fatalf("expected CI_PROJECT_DIR container to be included")
	}
	if !isLikelyGitLabJobContainer(map[string]string{"GITLAB_CI": "true"}) {
		t.Fatalf("expected GITLAB_CI=true container to be included")
	}
	if isLikelyGitLabJobContainer(map[string]string{"PATH": "/usr/bin"}) {
		t.Fatalf("expected non-CI container to be excluded")
	}
}

func TestSdkDockerProviderCachesExtraction(t *testing.T) {
	callCount := 0
	extractor := func(_ context.Context, _ string, _ string) (string, error) {
		callCount++
		return "/tmp/extracted/project", nil
	}

	mounts := []container.MountPoint{{Type: "volume", Destination: "/builds", Source: "/var/lib/docker/volumes/v1/_data"}}

	provider := &sdkDockerProvider{
		extractedDirs: make(map[string]string),
	}

	// First call: extraction should happen and be cached.
	got1, _, err := resolveSourceDirWithFallback(context.Background(), "cid-1", "/builds/group/project", mounts, extractor)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 1 {
		t.Fatalf("expected extractor called 1 time, got %d", callCount)
	}
	provider.extractedDirs["cid-1"] = got1

	// Second call: cache hit, extractor must not be called again.
	if cached, ok := provider.extractedDirs["cid-1"]; !ok || cached != got1 {
		t.Fatalf("expected cache hit for cid-1, got ok=%v val=%q", ok, cached)
	}
	if callCount != 1 {
		t.Fatalf("expected extractor still called only 1 time, got %d", callCount)
	}
}

func TestSdkDockerProviderPrunesStaleCache(t *testing.T) {
	provider := &sdkDockerProvider{
		extractedDirs: map[string]string{
			"cid-1": "/tmp/extracted/project-a",
			"cid-2": "/tmp/extracted/project-b",
		},
	}

	// Simulate: only cid-1 is still running.
	runningIDs := map[string]struct{}{"cid-1": {}}
	for id := range provider.extractedDirs {
		if _, ok := runningIDs[id]; !ok {
			delete(provider.extractedDirs, id)
		}
	}

	if _, ok := provider.extractedDirs["cid-2"]; ok {
		t.Fatalf("expected cid-2 to be pruned from extractedDirs")
	}
	if _, ok := provider.extractedDirs["cid-1"]; !ok {
		t.Fatalf("expected cid-1 to remain in extractedDirs")
	}
}

func TestSdkDockerProviderListRunningContainers(t *testing.T) {
	provider := &sdkDockerProvider{
		extractedDirs: map[string]string{"stale": "/tmp/old"},
		containerListFn: func(context.Context, container.ListOptions) ([]container.Summary, error) {
			return []container.Summary{{ID: "cid-1"}}, nil
		},
		containerInspectFn: func(context.Context, string) (container.InspectResponse, error) {
			return container.InspectResponse{
				ContainerJSONBase: &types.ContainerJSONBase{},
				Config: &container.Config{
					Env:        []string{"CI_JOB_ID=123", "CI_PROJECT_DIR=/builds/group/project"},
					Entrypoint: []string{"/usr/bin/dumb-init"},
					Cmd:        []string{"/entrypoint", "gitlab-runner-build"},
				},
				Mounts: []container.MountPoint{{Destination: "/builds", Source: "/host/builds", Type: "bind"}},
			}, nil
		},
	}

	jobs, err := provider.ListRunningContainers(context.Background())
	if err != nil {
		t.Fatalf("ListRunningContainers: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected one job, got %d", len(jobs))
	}
	if jobs[0].JobID != "123" {
		t.Fatalf("unexpected job id: %q", jobs[0].JobID)
	}
	if jobs[0].SourceDir != filepath.Join("/host/builds", "group", "project") {
		t.Fatalf("unexpected source dir: %q", jobs[0].SourceDir)
	}
	if _, ok := provider.extractedDirs["stale"]; ok {
		t.Fatalf("expected stale cache entry to be pruned")
	}
}

func TestSdkDockerProviderListRunningContainersNotInitialized(t *testing.T) {
	provider := &sdkDockerProvider{}
	_, err := provider.ListRunningContainers(context.Background())
	if err == nil {
		t.Fatalf("expected initialization error")
	}
}

func TestExtractContainerProjectDir(t *testing.T) {
	buf := &bytes.Buffer{}
	tw := tar.NewWriter(buf)
	if err := tw.WriteHeader(&tar.Header{Name: "project", Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
		t.Fatalf("write dir header: %v", err)
	}
	content := []byte("hello")
	if err := tw.WriteHeader(&tar.Header{Name: "project/README.md", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(content))}); err != nil {
		t.Fatalf("write file header: %v", err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatalf("write content: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}

	provider := &sdkDockerProvider{
		copyFromContainerFn: func(context.Context, string, string) (io.ReadCloser, container.PathStat, error) {
			return io.NopCloser(bytes.NewReader(buf.Bytes())), container.PathStat{}, nil
		},
	}

	out, err := provider.extractContainerProjectDir(context.Background(), "cid-1", "/builds/group/project")
	if err != nil {
		t.Fatalf("extractContainerProjectDir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "README.md")); err != nil {
		t.Fatalf("expected extracted file: %v", err)
	}
	_ = os.RemoveAll(filepath.Dir(out))
}

func TestExtractContainerProjectDirErrors(t *testing.T) {
	provider := &sdkDockerProvider{}
	if _, err := provider.extractContainerProjectDir(context.Background(), "cid", ""); err == nil {
		t.Fatalf("expected error for empty project dir")
	}

	provider.copyFromContainerFn = func(context.Context, string, string) (io.ReadCloser, container.PathStat, error) {
		return nil, container.PathStat{}, errors.New("copy failed")
	}
	if _, err := provider.extractContainerProjectDir(context.Background(), "cid", "/builds/group/project"); err == nil {
		t.Fatalf("expected copy error")
	}
}
