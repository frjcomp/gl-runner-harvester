package monitor

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/frjcomp/gl-runner-harvester/internal/detector"
	"github.com/rs/zerolog/log"
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
	cli           *client.Client
	extractedDirs map[string]string // cache: container ID -> resolved host source dir
}

type containerProjectDirExtractor func(ctx context.Context, containerID, containerProjectDir string) (string, error)

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

	return &sdkDockerProvider{cli: cli, extractedDirs: make(map[string]string)}, nil
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

	// Build a set of currently-running container IDs so stale cache entries can be pruned.
	runningIDs := make(map[string]struct{}, len(containers))
	for _, c := range containers {
		runningIDs[c.ID] = struct{}{}
	}
	for id := range s.extractedDirs {
		if _, ok := runningIDs[id]; !ok {
			delete(s.extractedDirs, id)
		}
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

		containerProjectDir := ciLookup(env, "CI_PROJECT_DIR")
		var sourceDir string
		if cached, ok := s.extractedDirs[c.ID]; ok {
			sourceDir = cached
		} else {
			var extracted bool
			var extractErr error
			sourceDir, extracted, extractErr = resolveSourceDirWithFallback(ctx, c.ID, containerProjectDir, inspect.Mounts, s.extractContainerProjectDir)
			if extractErr != nil {
				log.Debug().Err(extractErr).Str("container_id", c.ID).Str("container_project_dir", containerProjectDir).Msg("Docker project dir extraction fallback failed")
			} else if extracted {
				log.Debug().Str("container_id", c.ID).Str("source_dir", sourceDir).Msg("Docker project dir extraction fallback succeeded")
				s.extractedDirs[c.ID] = sourceDir
			}
		}
		if sourceDir != "" {
			env["CI_PROJECT_DIR"] = sourceDir
		} else if strings.TrimSpace(containerProjectDir) != "" {
			log.Warn().Str("container_id", c.ID).Str("container_project_dir", containerProjectDir).Msg("Unable to resolve CI_PROJECT_DIR to host path or extracted snapshot")
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
		if !hostSourceUsableForCopy(m) {
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
		if !hostSourceUsableForCopy(m) {
			continue
		}
		if strings.HasPrefix(m.Destination, "/builds") {
			return m.Source
		}
	}
	return ""
}

func hostSourceUsableForCopy(m container.MountPoint) bool {
	source := strings.TrimSpace(m.Source)
	if source == "" {
		return false
	}
	// Prefer bind mounts for host-side copy. Non-bind mounts (for example named
	// Docker volumes) should be harvested via container copy fallback.
	mountType := strings.TrimSpace(strings.ToLower(string(m.Type)))
	return mountType == "" || mountType == "bind"
}

func shouldExtractFromContainer(sourceDir, containerProjectDir string) bool {
	return strings.TrimSpace(sourceDir) == "" && strings.TrimSpace(containerProjectDir) != ""
}

func resolveSourceDirWithFallback(ctx context.Context, containerID, containerProjectDir string, mounts []container.MountPoint, extract containerProjectDirExtractor) (string, bool, error) {
	sourceDir := resolveContainerProjectDirToHost(containerProjectDir, mounts)
	if sourceDir == "" {
		sourceDir = dockerSourceDirFallback(mounts)
	}
	if !shouldExtractFromContainer(sourceDir, containerProjectDir) {
		return sourceDir, false, nil
	}
	if extract == nil {
		return sourceDir, true, fmt.Errorf("extractor is nil")
	}

	extractedDir, err := extract(ctx, containerID, containerProjectDir)
	if err != nil {
		return sourceDir, true, err
	}

	return extractedDir, true, nil
}

func (s *sdkDockerProvider) extractContainerProjectDir(ctx context.Context, containerID, containerProjectDir string) (string, error) {
	projectDir := strings.TrimSpace(containerProjectDir)
	if projectDir == "" {
		return "", fmt.Errorf("container project directory is empty")
	}

	r, _, err := s.cli.CopyFromContainer(ctx, containerID, projectDir)
	if err != nil {
		return "", err
	}
	defer r.Close()

	extractRoot, err := os.MkdirTemp("", "gl-runner-harvester-src-")
	if err != nil {
		return "", err
	}

	if err := extractTarArchive(r, extractRoot); err != nil {
		_ = os.RemoveAll(extractRoot)
		return "", err
	}

	resolved := inferExtractedProjectRoot(extractRoot, path.Base(path.Clean(projectDir)))
	if resolved == "" {
		_ = os.RemoveAll(extractRoot)
		return "", fmt.Errorf("extracted archive for %q was empty", projectDir)
	}

	return resolved, nil
}

func extractTarArchive(src io.Reader, destDir string) error {
	tr := tar.NewReader(src)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		cleanName, ok := safeTarPath(header.Name)
		if !ok {
			return fmt.Errorf("unsafe archive entry path %q", header.Name)
		}
		if cleanName == "." {
			continue
		}

		targetPath := filepath.Join(destDir, cleanName)
		rel, err := filepath.Rel(destDir, targetPath)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("archive entry escapes destination: %q", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, 0o700); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0o700); err != nil {
				return err
			}
			f, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				_ = f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		default:
			continue
		}
	}
}

func safeTarPath(name string) (string, bool) {
	clean := filepath.Clean(filepath.FromSlash(strings.TrimSpace(name)))
	if clean == "" {
		return "", false
	}
	if filepath.IsAbs(clean) {
		return "", false
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", false
	}
	return clean, true
}

func inferExtractedProjectRoot(extractRoot, projectBase string) string {
	if strings.TrimSpace(extractRoot) == "" {
		return ""
	}

	entries, err := os.ReadDir(extractRoot)
	if err != nil {
		return ""
	}
	if len(entries) == 0 {
		return ""
	}

	if len(entries) == 1 {
		entry := entries[0]
		if entry.IsDir() && (projectBase == "" || entry.Name() == projectBase) {
			return filepath.Join(extractRoot, entry.Name())
		}
	}

	return extractRoot
}
