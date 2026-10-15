package release

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

const (
	defaultAPIBaseURL = "https://api.github.com"
	defaultTimeout    = 3 * time.Second
	maxResponseBytes  = 1 << 20
)

var (
	ErrInvalidRepository = errors.New("invalid github repository")
	ErrReleaseNotFound   = errors.New("latest release not found")
)

type Result struct {
	Latest   string
	Outdated bool
}

type Checker struct {
	client     *http.Client
	apiBaseURL string
}

type Option func(*Checker)

func WithHTTPClient(client *http.Client) Option {
	return func(c *Checker) {
		c.client = client
	}
}

func WithAPIBaseURL(baseURL string) Option {
	return func(c *Checker) {
		c.apiBaseURL = strings.TrimRight(baseURL, "/")
	}
}

func NewChecker(opts ...Option) *Checker {
	c := &Checker{
		client:     &http.Client{Timeout: defaultTimeout},
		apiBaseURL: defaultAPIBaseURL,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *Checker) Check(ctx context.Context, localVersion, repoURL string) (Result, error) {
	owner, repo, err := ParseRepositoryURL(repoURL)
	if err != nil {
		return Result{}, err
	}

	latest, err := c.latestTag(ctx, owner, repo)
	if err != nil {
		return Result{}, err
	}

	return Result{Latest: latest, Outdated: IsOutdated(localVersion, latest)}, nil
}

func (c *Checker) latestTag(ctx context.Context, owner, repo string) (string, error) {
	endpoint := fmt.Sprintf("%s/repos/%s/%s/releases/latest", c.apiBaseURL, owner, repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return "", ErrReleaseNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status from github: %s", resp.Status)
	}

	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&payload); err != nil {
		return "", err
	}
	if strings.TrimSpace(payload.TagName) == "" {
		return "", ErrReleaseNotFound
	}
	return payload.TagName, nil
}

func ParseRepositoryURL(repoURL string) (string, string, error) {
	trimmed := strings.TrimSuffix(strings.TrimSpace(repoURL), "/")
	trimmed = strings.TrimSuffix(trimmed, ".git")
	path, ok := strings.CutPrefix(trimmed, "https://github.com/")
	if !ok {
		return "", "", fmt.Errorf("%w: %q", ErrInvalidRepository, repoURL)
	}
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("%w: %q", ErrInvalidRepository, repoURL)
	}
	return parts[0], parts[1], nil
}

func IsOutdated(localVersion, latestVersion string) bool {
	local := canonical(localVersion)
	latest := canonical(latestVersion)
	if semver.IsValid(local) && semver.IsValid(latest) {
		return semver.Compare(local, latest) < 0
	}
	return strings.TrimSpace(localVersion) != strings.TrimSpace(latestVersion)
}

func canonical(version string) string {
	trimmed := strings.TrimSpace(version)
	if trimmed == "" || strings.HasPrefix(trimmed, "v") {
		return trimmed
	}
	return "v" + trimmed
}
