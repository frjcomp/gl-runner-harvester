package monitor

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/frjcomp/gl-runner-harvester/internal/detector"
)

type dockerContainerProvider interface {
	ListRunningContainers(ctx context.Context) ([]discoveredJob, error)
	Close() error
}

type dockerDiscoveryStrategy struct {
	provider dockerContainerProvider
}

func (d *dockerDiscoveryStrategy) Mode() string {
	return "docker-api"
}

func (d *dockerDiscoveryStrategy) Discover(ctx context.Context) ([]discoveredJob, error) {
	return d.provider.ListRunningContainers(ctx)
}

func defaultDockerStrategy(osInfo detector.OSInfo) (*strategyWithCloser, error) {
	provider, err := newDockerProvider(osInfo)
	if err != nil {
		return nil, err
	}
	return &strategyWithCloser{
		discoveryStrategy: &dockerDiscoveryStrategy{provider: provider},
		closer:            provider,
	}, nil
}

type sdkDockerProvider struct {
	cli *client.Client
}

func newDockerProvider(osInfo detector.OSInfo) (*sdkDockerProvider, error) {
	host, err := dockerHostForOS(osInfo.OS)
	if err != nil {
		return nil, err
	}

	cli, err := client.NewClientWithOpts(
		client.WithHost(host),
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, err
	}

	if _, err := cli.Ping(context.Background()); err != nil {
		_ = cli.Close()
		return nil, fmt.Errorf("docker daemon ping failed: %w", err)
	}

	return &sdkDockerProvider{cli: cli}, nil
}

func dockerHostForOS(goos string) (string, error) {
	host := strings.TrimSpace(os.Getenv("DOCKER_HOST"))
	if host == "" {
		if goos == "windows" {
			return "npipe:////./pipe/docker_engine", nil
		}
		return "unix:///var/run/docker.sock", nil
	}

	if strings.HasPrefix(host, "unix://") || strings.HasPrefix(host, "npipe://") || strings.HasPrefix(host, "tcp://") {
		return host, nil
	}

	return "", fmt.Errorf("unsupported DOCKER_HOST %q: supported schemes are unix://, npipe://, tcp://", host)
}

func (s *sdkDockerProvider) Close() error {
	return s.cli.Close()
}

func (s *sdkDockerProvider) ListRunningContainers(ctx context.Context) ([]discoveredJob, error) {
	containers, err := s.cli.ContainerList(ctx, container.ListOptions{All: false})
	if err != nil {
		return nil, err
	}

	out := make([]discoveredJob, 0, len(containers))
	for _, c := range containers {
		inspect, err := s.cli.ContainerInspect(ctx, c.ID)
		if err != nil {
			continue
		}

		env := map[string]string{}
		if inspect.Config != nil {
			env = envListToMap(inspect.Config.Env)
		}

		if !isLikelyGitLabJobContainer(env) {
			continue
		}

		jobID := strings.TrimSpace(ciLookup(env, "CI_JOB_ID"))
		if jobID == "" {
			jobID = c.ID
		}

		sourceDir := resolveContainerProjectDirToHost(ciLookup(env, "CI_PROJECT_DIR"), inspect.Mounts)
		if sourceDir == "" {
			sourceDir = dockerSourceDirFallback(inspect.Mounts)
		}
		if sourceDir != "" {
			env["CI_PROJECT_DIR"] = sourceDir
		}

		cmdline := ""
		if inspect.Config != nil {
			cmdline = strings.Join(append(inspect.Config.Entrypoint, inspect.Config.Cmd...), " ")
		}

		out = append(out, discoveredJob{
			Identity:      "docker:" + jobID,
			JobID:         jobID,
			Cmdline:       strings.TrimSpace(cmdline),
			Env:           env,
			SourceDir:     sourceDir,
			DiscoveryMode: "docker-api",
		})
	}

	return out, nil
}

func isLikelyGitLabJobContainer(env map[string]string) bool {
	if strings.TrimSpace(ciLookup(env, "CI_JOB_ID")) != "" {
		return true
	}
	if strings.TrimSpace(ciLookup(env, "CI_PROJECT_DIR")) != "" {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(ciLookup(env, "GITLAB_CI")), "true") {
		return true
	}
	return false
}

func resolveContainerProjectDirToHost(containerProjectDir string, mounts []container.MountPoint) string {
	projectDir := path.Clean(strings.TrimSpace(containerProjectDir))
	if projectDir == "" || projectDir == "." {
		return ""
	}

	bestDest := ""
	bestSource := ""
	for _, m := range mounts {
		if strings.TrimSpace(m.Source) == "" {
			continue
		}
		dest := path.Clean(strings.TrimSpace(m.Destination))
		if dest == "" || dest == "." {
			continue
		}
		if projectDir != dest && !strings.HasPrefix(projectDir, dest+"/") {
			continue
		}
		if len(dest) > len(bestDest) {
			bestDest = dest
			bestSource = m.Source
		}
	}

	if bestDest == "" {
		return ""
	}

	rel := strings.TrimPrefix(projectDir, bestDest)
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" {
		return bestSource
	}

	return filepath.Join(bestSource, filepath.FromSlash(rel))
}

func dockerSourceDirFallback(mounts []container.MountPoint) string {
	for _, m := range mounts {
		if m.Source == "" {
			continue
		}
		if strings.HasPrefix(m.Destination, "/builds") {
			return m.Source
		}
	}
	return ""
}
