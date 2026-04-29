package retriever

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// RegistryRetriever fetches GitLab container registry information
// using a CI_JOB_TOKEN.
type RegistryRetriever struct {
	gitlabURL  string
	httpClient *http.Client
}

// NewRegistry creates a RegistryRetriever for the given GitLab base URL.
func NewRegistry(gitlabURL string) *RegistryRetriever {
	return &RegistryRetriever{
		gitlabURL:  strings.TrimRight(gitlabURL, "/"),
		httpClient: &http.Client{},
	}
}

type registryRepository struct {
	ID   int    `json:"id"`
	Path string `json:"path"`
}

type registryTag struct {
	Name string `json:"name"`
}

// LatestImageRef returns the full image reference (registry/path:tag) of the
// most recently pushed tag across all registry repositories for the project.
// Returns an empty string (no error) when the project has no registry images.
func (r *RegistryRetriever) LatestImageRef(ctx context.Context, token, projectID, registry string) (string, error) {
	repos, err := r.listRepositories(ctx, token, projectID)
	if err != nil {
		return "", fmt.Errorf("list registry repositories: %w", err)
	}
	if len(repos) == 0 {
		return "", nil
	}

	// Use the first (primary) repository.
	repo := repos[0]

	tag, err := r.latestTag(ctx, token, projectID, repo.ID)
	if err != nil {
		return "", fmt.Errorf("list tags for repo %d: %w", repo.ID, err)
	}
	if tag == "" {
		return "", nil
	}

	registryHost := strings.TrimRight(registry, "/")
	return fmt.Sprintf("%s/%s:%s", registryHost, repo.Path, tag), nil
}

func (r *RegistryRetriever) listRepositories(ctx context.Context, token, projectID string) ([]registryRepository, error) {
	endpoint := fmt.Sprintf("%s/api/v4/projects/%s/registry/repositories", r.gitlabURL, projectID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("JOB-TOKEN", token)

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusForbidden {
		// Registry not enabled / no access — not an error for the caller.
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d listing repositories", resp.StatusCode)
	}

	var repos []registryRepository
	if err := json.NewDecoder(resp.Body).Decode(&repos); err != nil {
		return nil, fmt.Errorf("decode repositories response: %w", err)
	}
	return repos, nil
}

// latestTag returns the name of the most recently updated tag, or "" if none.
func (r *RegistryRetriever) latestTag(ctx context.Context, token, projectID string, repoID int) (string, error) {
	endpoint := fmt.Sprintf(
		"%s/api/v4/projects/%s/registry/repositories/%d/tags?sort=desc&order_by=updated_at&per_page=1",
		r.gitlabURL, projectID, repoID,
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("JOB-TOKEN", token)

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d listing tags", resp.StatusCode)
	}

	var tags []registryTag
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return "", fmt.Errorf("decode tags response: %w", err)
	}
	if len(tags) == 0 {
		return "", nil
	}
	return tags[0].Name, nil
}
