package monitor

import (
	"testing"

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
