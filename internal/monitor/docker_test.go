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
