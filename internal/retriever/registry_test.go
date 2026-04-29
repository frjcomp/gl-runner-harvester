package retriever

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRegistryLatestImageRef(t *testing.T) {
	token := "job-token"
	projectID := "55"
	registryHost := "registry.example.com"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("JOB-TOKEN"); got != token {
			t.Fatalf("expected JOB-TOKEN header %q, got %q", token, got)
		}

		switch r.URL.Path {
		case "/api/v4/projects/55/registry/repositories":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"id":7,"path":"group/project/app"}]`))
		case "/api/v4/projects/55/registry/repositories/7/tags":
			if got := r.URL.Query().Get("order_by"); got != "updated_at" {
				t.Fatalf("expected order_by=updated_at, got %q", got)
			}
			if got := r.URL.Query().Get("sort"); got != "desc" {
				t.Fatalf("expected sort=desc, got %q", got)
			}
			if got := r.URL.Query().Get("per_page"); got != "1" {
				t.Fatalf("expected per_page=1, got %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"name":"release-2026.04"}]`))
		default:
			t.Fatalf("unexpected request path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	r := NewRegistry(server.URL)
	imageRef, err := r.LatestImageRef(context.Background(), token, projectID, registryHost)
	if err != nil {
		t.Fatalf("LatestImageRef: %v", err)
	}
	if imageRef != "registry.example.com/group/project/app:release-2026.04" {
		t.Fatalf("unexpected image ref %q", imageRef)
	}
}

func TestRegistryLatestImageRefNoRepositories(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/projects/55/registry/repositories" {
			t.Fatalf("unexpected request path %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	r := NewRegistry(server.URL)
	imageRef, err := r.LatestImageRef(context.Background(), "token", "55", "registry.example.com")
	if err != nil {
		t.Fatalf("LatestImageRef: %v", err)
	}
	if imageRef != "" {
		t.Fatalf("expected empty image ref, got %q", imageRef)
	}
}
