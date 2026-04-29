package retriever

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSecureFilesFetchAll(t *testing.T) {
	token := "job-token"
	projectID := "123"
	tmp := t.TempDir()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("JOB-TOKEN"); got != token {
			t.Fatalf("expected JOB-TOKEN header %q, got %q", token, got)
		}

		switch r.URL.Path {
		case "/api/v4/projects/123/secure_files":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"id":1,"name":"secrets.env"},{"id":2,"name":"nested/config.json"}]`))
		case "/api/v4/projects/123/secure_files/1/download":
			_, _ = w.Write([]byte("A=1\n"))
		case "/api/v4/projects/123/secure_files/2/download":
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			t.Fatalf("unexpected request path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	r := NewSecureFiles(server.URL)
	if err := r.FetchAll(context.Background(), token, projectID, tmp); err != nil {
		t.Fatalf("FetchAll: %v", err)
	}

	first, err := os.ReadFile(filepath.Join(tmp, "secrets.env"))
	if err != nil {
		t.Fatalf("read first secure file: %v", err)
	}
	if got := string(first); got != "A=1\n" {
		t.Fatalf("unexpected first file contents %q", got)
	}

	second, err := os.ReadFile(filepath.Join(tmp, "config.json"))
	if err != nil {
		t.Fatalf("read second secure file: %v", err)
	}
	if got := string(second); got != `{"ok":true}` {
		t.Fatalf("unexpected second file contents %q", got)
	}
	if _, err := os.Stat(filepath.Join(tmp, "nested")); !os.IsNotExist(err) {
		t.Fatalf("expected nested directory to not be created, err=%v", err)
	}
}

func TestSecureFilesFetchAllEmptyList(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "secure_files")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/projects/123/secure_files" {
			t.Fatalf("unexpected request path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	r := NewSecureFiles(server.URL)
	if err := r.FetchAll(context.Background(), "token", "123", tmp); err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("expected no output directory for empty secure files list, err=%v", err)
	}
}
