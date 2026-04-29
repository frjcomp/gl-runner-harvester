package harvester

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/registry"
	"github.com/docker/docker/client"
	"github.com/rs/zerolog/log"
)

var imageArchiveUnsafeChars = regexp.MustCompile(`[^[:alnum:]._-]+`)

// HarvestImage authenticates to the GitLab container registry using
// CI_JOB_TOKEN, pulls imageRef via the local Docker daemon, saves it using a
// filesystem-safe archive name derived from the image tag inside destDir, and
// removes the locally cached image afterwards.
func HarvestImage(ctx context.Context, env map[string]string, imageRef, destDir string) error {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("create docker client: %w", err)
	}
	defer cli.Close()

	registryHost := strings.TrimRight(env["CI_REGISTRY"], "/")
	token := env["CI_JOB_TOKEN"]

	if registryHost != "" && token != "" {
		authConfig := registry.AuthConfig{
			Username:      "gitlab-ci-token",
			Password:      token,
			ServerAddress: registryHost,
		}
		encodedAuth, err := encodeAuth(authConfig)
		if err != nil {
			return fmt.Errorf("encode registry auth: %w", err)
		}

		pullResp, err := cli.ImagePull(ctx, imageRef, image.PullOptions{RegistryAuth: encodedAuth})
		if err != nil {
			return fmt.Errorf("pull image %q: %w", imageRef, err)
		}
		// Drain pull output so the daemon completes the pull.
		if _, err := io.Copy(io.Discard, pullResp); err != nil {
			pullResp.Close()
			return fmt.Errorf("drain pull response: %w", err)
		}
		pullResp.Close()
		log.Info().Str("image", imageRef).Msg("Image pulled")
	} else {
		log.Warn().Str("image", imageRef).Msg("CI_REGISTRY or CI_JOB_TOKEN missing; skipping registry login, attempting pull with existing daemon credentials")
		pullResp, err := cli.ImagePull(ctx, imageRef, image.PullOptions{})
		if err != nil {
			return fmt.Errorf("pull image %q: %w", imageRef, err)
		}
		if _, err := io.Copy(io.Discard, pullResp); err != nil {
			pullResp.Close()
			return fmt.Errorf("drain pull response: %w", err)
		}
		pullResp.Close()
	}

	if err := os.MkdirAll(destDir, 0700); err != nil {
		return fmt.Errorf("create image dir: %w", err)
	}

	saveResp, err := cli.ImageSave(ctx, []string{imageRef})
	if err != nil {
		return fmt.Errorf("save image %q: %w", imageRef, err)
	}
	defer saveResp.Close()

	outPath := filepath.Join(destDir, imageArchiveName(imageRef))
	out, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("create image archive: %w", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, saveResp); err != nil {
		return fmt.Errorf("write image archive: %w", err)
	}
	log.Info().Str("path", outPath).Str("image", imageRef).Msg("Image saved")

	// Clean up the pulled image from the daemon.
	if _, err := cli.ImageRemove(ctx, imageRef, image.RemoveOptions{Force: false}); err != nil {
		log.Warn().Err(err).Str("image", imageRef).Msg("Could not remove pulled image from daemon cache")
	}

	return nil
}

func encodeAuth(authConfig registry.AuthConfig) (string, error) {
	authJSON, err := json.Marshal(authConfig)
	if err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(authJSON), nil
}

func imageArchiveName(imageRef string) string {
	tag := imageTag(imageRef)
	if tag == "" {
		tag = "image"
	}

	name := imageArchiveUnsafeChars.ReplaceAllString(tag, "_")
	name = strings.Trim(name, "._-")
	if name == "" {
		name = "image"
	}

	return name + ".tar"
}

func imageTag(imageRef string) string {
	trimmed := strings.TrimSpace(imageRef)
	if digestIndex := strings.Index(trimmed, "@"); digestIndex >= 0 {
		trimmed = trimmed[:digestIndex]
	}

	lastSlash := strings.LastIndex(trimmed, "/")
	tail := trimmed
	if lastSlash >= 0 {
		tail = trimmed[lastSlash+1:]
	}

	colonIndex := strings.Index(tail, ":")
	if colonIndex < 0 {
		return ""
	}
	return strings.TrimSpace(tail[colonIndex+1:])
}
