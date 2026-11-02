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

// defaultAPIBaseURL is the GitHub REST endpoint used in production. Tests
// point the client at a local server instead so the shard listing and download
// paths can be exercised without touching the network.
const defaultAPIBaseURL = "https://api.github.com"

type GitHubClient struct {
	client           *http.Client
	baseURL          string
	token            string
	maxDownloadBytes int64
}

func NewGitHubClient(token string, maxDownloadBytes int64) *GitHubClient {
	return &GitHubClient{
		client:  &http.Client{Timeout: 2 * time.Minute},
		baseURL: defaultAPIBaseURL,
		token:   token, maxDownloadBytes: maxDownloadBytes,
	}
}

func (g *GitHubClient) LatestCommit(ctx context.Context, repository, branch, filePath string) (Commit, error) {
	endpoint := fmt.Sprintf("%s/repos/%s/commits?path=%s&sha=%s&per_page=1",
		g.baseURL, escapeRepository(repository), url.QueryEscape(filePath), url.QueryEscape(branch))
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

// ListDirectory returns the repository-relative paths of the regular files in
// a directory at a specific commit, in the order the Contents API returns them
// (which GitHub sorts by name).
//
// Sharded sources point their path at a directory of CSV files instead of a
// single file. The Contents API answers a directory with a JSON array and a
// file with a JSON object, so isDirectory reports which shape came back and a
// single-file source keeps working unchanged.
//
// The listing is pinned to the same commit SHA as the import, so every shard of
// one import comes from one consistent snapshot of the repository.
func (g *GitHubClient) ListDirectory(ctx context.Context, repository, directory, sha string) (paths []string, isDirectory bool, err error) {
	endpoint := fmt.Sprintf("%s/repos/%s/contents/%s?ref=%s",
		g.baseURL, escapeRepository(repository), escapePath(directory), url.QueryEscape(sha))
	request, err := g.request(ctx, http.MethodGet, endpoint)
	if err != nil {
		return nil, false, err
	}
	response, err := g.client.Do(request)
	if err != nil {
		return nil, false, fmt.Errorf("list GitHub directory: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, false, responseError("list GitHub directory", response)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return nil, false, fmt.Errorf("read GitHub directory listing: %w", err)
	}
	var entries []struct {
		Type string `json:"type"`
		Path string `json:"path"`
	}
	if err := json.Unmarshal(body, &entries); err != nil {
		// Not an array: the configured path is a single file.
		return nil, false, nil
	}
	for _, entry := range entries {
		if entry.Type == "file" && entry.Path != "" {
			paths = append(paths, entry.Path)
		}
	}
	if len(paths) == 0 {
		return nil, true, fmt.Errorf("GitHub directory %q contains no files", directory)
	}
	return paths, true, nil
}

// DownloadAll downloads every listed file into its own temporary file. The
// combined size is capped by MAX_DOWNLOAD_BYTES so a directory source cannot
// fill the instance's disk, and a failure part-way leaves no files behind.
func (g *GitHubClient) DownloadAll(ctx context.Context, repository string, filePaths []string, sha string) ([]*os.File, error) {
	files := make([]*os.File, 0, len(filePaths))
	removeAll := func() {
		for _, file := range files {
			removeFile(file)
		}
	}
	var total int64
	for _, filePath := range filePaths {
		file, err := g.Download(ctx, repository, filePath, sha)
		if err != nil {
			removeAll()
			return nil, err
		}
		info, err := file.Stat()
		if err != nil {
			removeFile(file)
			removeAll()
			return nil, err
		}
		total += info.Size()
		if total > g.maxDownloadBytes {
			removeFile(file)
			removeAll()
			return nil, fmt.Errorf("directory source is over MAX_DOWNLOAD_BYTES (%d bytes)", g.maxDownloadBytes)
		}
		files = append(files, file)
	}
	return files, nil
}

// Download writes a source file to a temporary file. Downloading before opening
// the database transaction means a slow upstream never holds database locks.
func (g *GitHubClient) Download(ctx context.Context, repository, filePath, sha string) (*os.File, error) {
	endpoint := fmt.Sprintf("%s/repos/%s/contents/%s?ref=%s",
		g.baseURL, escapeRepository(repository), escapePath(filePath), url.QueryEscape(sha))
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
