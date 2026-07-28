package syncer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"
)

type Commit struct {
	SHA  string
	Date time.Time
}

type GitHubClient struct {
	client           *http.Client
	token            string
	maxDownloadBytes int64
}

func NewGitHubClient(token string, maxDownloadBytes int64) *GitHubClient {
	return &GitHubClient{
		client: &http.Client{Timeout: 2 * time.Minute},
		token:  token, maxDownloadBytes: maxDownloadBytes,
	}
}

func (g *GitHubClient) LatestCommit(ctx context.Context, repository, branch, filePath string) (Commit, error) {
	endpoint := fmt.Sprintf("https://api.github.com/repos/%s/commits?path=%s&sha=%s&per_page=1",
		escapeRepository(repository), url.QueryEscape(filePath), url.QueryEscape(branch))
	request, err := g.request(ctx, http.MethodGet, endpoint)
	if err != nil {
		return Commit{}, err
	}
	response, err := g.client.Do(request)
	if err != nil {
		return Commit{}, fmt.Errorf("query GitHub commit: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Commit{}, responseError("query GitHub commit", response)
	}
	var payload []struct {
		SHA    string `json:"sha"`
		Commit struct {
			Author struct {
				Date time.Time `json:"date"`
			} `json:"author"`
		} `json:"commit"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload); err != nil {
		return Commit{}, fmt.Errorf("decode GitHub commit: %w", err)
	}
	if len(payload) == 0 || payload[0].SHA == "" {
		return Commit{}, errors.New("GitHub returned no commit for configured source path")
	}
	return Commit{SHA: payload[0].SHA, Date: payload[0].Commit.Author.Date}, nil
}

// Download writes a source file to a temporary file. Downloading before opening
// the database transaction means a slow upstream never holds database locks.
func (g *GitHubClient) Download(ctx context.Context, repository, filePath, sha string) (*os.File, error) {
	endpoint := fmt.Sprintf("https://api.github.com/repos/%s/contents/%s?ref=%s",
		escapeRepository(repository), escapePath(filePath), url.QueryEscape(sha))
	request, err := g.request(ctx, http.MethodGet, endpoint)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/vnd.github.raw+json")
	response, err := g.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("download GitHub source: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, responseError("download GitHub source", response)
	}
	if response.ContentLength > g.maxDownloadBytes {
		return nil, fmt.Errorf("source is %d bytes, over MAX_DOWNLOAD_BYTES", response.ContentLength)
	}

	file, err := os.CreateTemp("", "open-bin-source-*.csv")
	if err != nil {
		return nil, err
	}
	removeOnError := func(err error) (*os.File, error) {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return nil, err
	}
	written, err := io.Copy(file, io.LimitReader(response.Body, g.maxDownloadBytes+1))
	if err != nil {
		return removeOnError(fmt.Errorf("save GitHub source: %w", err))
	}
	if written > g.maxDownloadBytes {
		return removeOnError(fmt.Errorf("source exceeds MAX_DOWNLOAD_BYTES (%d bytes)", g.maxDownloadBytes))
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return removeOnError(err)
	}
	return file, nil
}

func (g *GitHubClient) request(ctx context.Context, method, endpoint string) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "open-bin-api/1.0")
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if g.token != "" {
		request.Header.Set("Authorization", "Bearer "+g.token)
	}
	return request, nil
}

func responseError(action string, response *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(response.Body, 4<<10))
	message := strings.TrimSpace(string(body))
	if message == "" {
		message = response.Status
	}
	return fmt.Errorf("%s: GitHub returned %d: %s", action, response.StatusCode, message)
}

func escapeRepository(repository string) string {
	parts := strings.Split(repository, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}

func escapePath(filePath string) string {
	cleaned := strings.TrimPrefix(path.Clean("/"+filePath), "/")
	parts := strings.Split(cleaned, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}
