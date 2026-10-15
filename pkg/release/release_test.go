package release

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseRepositoryURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		owner   string
		repo    string
		wantErr bool
	}{
		{name: "plain", url: "https://github.com/TNK-Studio/lazykube", owner: "TNK-Studio", repo: "lazykube"},
		{name: "trailing slash", url: "https://github.com/TNK-Studio/lazykube/", owner: "TNK-Studio", repo: "lazykube"},
		{name: "git suffix", url: "https://github.com/TNK-Studio/lazykube.git", owner: "TNK-Studio", repo: "lazykube"},
		{name: "other host", url: "https://gitlab.com/a/b", wantErr: true},
		{name: "missing repo", url: "https://github.com/TNK-Studio", wantErr: true},
		{name: "extra path", url: "https://github.com/a/b/c", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owner, repo, err := ParseRepositoryURL(tt.url)
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidRepository) {
					t.Fatalf("expected ErrInvalidRepository, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if owner != tt.owner || repo != tt.repo {
				t.Fatalf("got %s/%s, want %s/%s", owner, repo, tt.owner, tt.repo)
			}
		})
	}
}

func TestIsOutdated(t *testing.T) {
	tests := []struct {
		local, latest string
		want          bool
	}{
		{"v0.10.0", "v0.11.0", true},
		{"0.10.0", "v0.11.0", true},
		{"v0.11.0", "v0.11.0", false},
		{"v0.12.0", "v0.11.0", false},
		{"v0.9.0", "v0.10.0", true},
		{"No Version Provided", "v0.11.0", true},
		{"dev", "dev", false},
	}
	for _, tt := range tests {
		if got := IsOutdated(tt.local, tt.latest); got != tt.want {
			t.Errorf("IsOutdated(%q, %q) = %v, want %v", tt.local, tt.latest, got, tt.want)
		}
	}
}

func newTestChecker(t *testing.T, handler http.HandlerFunc) *Checker {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return NewChecker(WithHTTPClient(server.Client()), WithAPIBaseURL(server.URL))
}

func TestCheckerCheck(t *testing.T) {
	checker := newTestChecker(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/TNK-Studio/lazykube/releases/latest" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"tag_name":"v0.11.0"}`))
	})

	result, err := checker.Check(context.Background(), "v0.10.0", "https://github.com/TNK-Studio/lazykube")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Outdated || result.Latest != "v0.11.0" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestCheckerCheckNotFound(t *testing.T) {
	checker := newTestChecker(t, http.NotFound)

	_, err := checker.Check(context.Background(), "v0.10.0", "https://github.com/TNK-Studio/lazykube")
	if !errors.Is(err, ErrReleaseNotFound) {
		t.Fatalf("expected ErrReleaseNotFound, got %v", err)
	}
}

func TestCheckerCheckServerError(t *testing.T) {
	checker := newTestChecker(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	if _, err := checker.Check(context.Background(), "v0.10.0", "https://github.com/TNK-Studio/lazykube"); err == nil {
		t.Fatal("expected error")
	}
}

func TestCheckerCheckEmptyTag(t *testing.T) {
	checker := newTestChecker(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":""}`))
	})

	_, err := checker.Check(context.Background(), "v0.10.0", "https://github.com/TNK-Studio/lazykube")
	if !errors.Is(err, ErrReleaseNotFound) {
		t.Fatalf("expected ErrReleaseNotFound, got %v", err)
	}
}
