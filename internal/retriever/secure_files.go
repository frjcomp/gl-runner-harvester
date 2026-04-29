package retriever

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// SecureFilesRetriever fetches GitLab secure files using a CI_JOB_TOKEN.
type SecureFilesRetriever struct {
	gitlabURL  string
	httpClient *http.Client
}

// NewSecureFiles creates a SecureFilesRetriever for the given GitLab base URL.
func NewSecureFiles(gitlabURL string) *SecureFilesRetriever {
	return &SecureFilesRetriever{
		gitlabURL:  strings.TrimRight(gitlabURL, "/"),
		httpClient: &http.Client{},
	}
}

type secureFileMeta struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// FetchAll downloads all secure files for a project into destDir.
// token is CI_JOB_TOKEN and projectID is CI_PROJECT_ID.
func (r *SecureFilesRetriever) FetchAll(ctx context.Context, token, projectID, destDir string) error {
	files, err := r.listFiles(ctx, token, projectID)
	if err != nil {
		return fmt.Errorf("list secure files: %w", err)
	}
	if len(files) == 0 {
		return nil
	}
	if err := os.MkdirAll(destDir, 0700); err != nil {
		return fmt.Errorf("create secure_files dir: %w", err)
	}
	for _, f := range files {
		if err := r.downloadFile(ctx, token, projectID, f, destDir); err != nil {
			return fmt.Errorf("download secure file %q: %w", f.Name, err)
		}
	}
	return nil
}

func (r *SecureFilesRetriever) listFiles(ctx context.Context, token, projectID string) ([]secureFileMeta, error) {
	endpoint := fmt.Sprintf("%s/api/v4/projects/%s/secure_files", r.gitlabURL, projectID)
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

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d listing secure files", resp.StatusCode)
	}

	var files []secureFileMeta
	if err := json.NewDecoder(resp.Body).Decode(&files); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return files, nil
}

func (r *SecureFilesRetriever) downloadFile(ctx context.Context, token, projectID string, f secureFileMeta, destDir string) error {
	endpoint := fmt.Sprintf("%s/api/v4/projects/%s/secure_files/%d/download", r.gitlabURL, projectID, f.ID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("JOB-TOKEN", token)

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d downloading file %q", resp.StatusCode, f.Name)
	}

	// Sanitize filename — no path traversal.
	safeName := filepath.Base(f.Name)
	if safeName == "." || safeName == ".." || safeName == "" {
		safeName = fmt.Sprintf("secure_file_%d", f.ID)
	}

	out, err := os.OpenFile(filepath.Join(destDir, safeName), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, resp.Body); err != nil {
		return fmt.Errorf("write file: %w", err)
	}
	return nil
}
