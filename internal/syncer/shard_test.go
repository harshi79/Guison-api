package syncer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/harshi79/project-17/internal/config"
)

// shardedSourceHarness serves the three GitHub REST calls a sharded OpenBIIN
// source makes: the commit lookup for the configured path, the directory
// listing, and each shard's raw bytes. Nothing here touches the network.
type shardedSourceHarness struct {
	commitSHA string
	commitAt  time.Time
	dir       string
	shards    map[string]string // file name -> CSV body
	contents  map[string][]byte // full repository path -> bytes to serve
	requests  []string
}

func (h *shardedSourceHarness) start(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.requests = append(h.requests, r.URL.Path)
		switch {
		case strings.HasSuffix(r.URL.Path, "/commits"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"sha":    h.commitSHA,
				"commit": map[string]any{"author": map[string]any{"date": h.commitAt}},
			}})
		case strings.HasSuffix(r.URL.Path, "/contents/"+h.dir):
			names := make([]string, 0, len(h.shards))
			for name := range h.shards {
				names = append(names, name)
			}
			entries := make([]map[string]any, 0, len(names))
			for _, name := range names {
				entries = append(entries, map[string]any{
					"type": "file", "name": name, "path": h.dir + "/" + name,
					"size": len(h.shards[name]),
				})
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(entries)
		default:
			if body, ok := h.contents[r.URL.Path]; ok {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(body)
				return
			}
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func openbiinShard(rows ...string) string {
	body := "BIN6,Ranges,Issuer,Country,Brand,Type\n"
	return body + strings.Join(rows, "\n") + "\n"
}

// TestShardedSourceImportsEveryFile drives a full automatic sync of a
// directory-backed source: one commit check, one directory listing, one
// download per shard, and one transaction spanning them all.
func TestShardedSourceImportsEveryFile(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	harness := &shardedSourceHarness{
		commitSHA: "sharded-commit-1",
		commitAt:  time.Date(2026, 9, 21, 3, 23, 42, 0, time.UTC),
		dir:       "functions/data",
		shards: map[string]string{
			"00.csv": openbiinShard("002102,00-99,CHINA MERCHANTS BANK,CN,private label,credit"),
			"45.csv": openbiinShard(
				"457100,40-45|51-53,NORDEA,DK,visa,debit",
				"457101,05,SINGLE BANK,US,visa,credit",
			),
		},
	}
	harness.contents = map[string][]byte{
		"/repos/owner/openbiin/contents/functions/data/00.csv": []byte(harness.shards["00.csv"]),
		"/repos/owner/openbiin/contents/functions/data/45.csv": []byte(harness.shards["45.csv"]),
	}
	server := harness.start(t)

	source := config.Source{
		ID: "openbiin", Repository: "owner/openbiin", Branch: "main",
		Path: "functions/data", Format: "openbiin", Priority: 50, MinRecords: 1,
	}
	if err := db.ConfigureSources(ctx, []config.Source{source}); err != nil {
		t.Fatalf("configure sources: %v", err)
	}
	github := NewGitHubClient("", 100<<20)
	github.baseURL = server.URL

	manager := New(db.DB(), github, []config.Source{source}, 0, 0.05, 100<<20)
	if err := manager.Sync(ctx, source); err != nil {
		t.Fatalf("sync sharded source: %v", err)
	}

	// 1 full-range row + 2 blocks on one row + 1 single-value row = 4 records
	// from two separate CSV files, all installed by one transaction.
	var rows int64
	if err := db.DB().QueryRow(`SELECT count(*) FROM bin_records WHERE source_id=?1`, source.ID).Scan(&rows); err != nil {
		t.Fatalf("count records: %v", err)
	}
	if rows != 4 {
		t.Errorf("installed %d records, want 4 (one shard alone would give 1 or 3)", rows)
	}

	var status, sha string
	var recordCount int64
	if err := db.DB().QueryRow(`SELECT status, current_sha, record_count FROM sources WHERE id=?1`, source.ID).
		Scan(&status, &sha, &recordCount); err != nil {
		t.Fatalf("read source: %v", err)
	}
	if status != "ready" || sha != harness.commitSHA || recordCount != 4 {
		t.Errorf("source state = %q/%q/%d, want ready/%s/4", status, sha, recordCount, harness.commitSHA)
	}

	result, err := db.Lookup(ctx, "45710042")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if result == nil || result.Source.ID != "openbiin" || result.Match.Start != "45710040" {
		t.Fatalf("lookup 45710042 = %+v, want the sharded source's 45710040 sub-range", result)
	}
}

// TestShardedSourceRollsBackWhenAShardIsUnusable proves the shards of one
// directory are one atomic unit: if the last file fails to parse, the records
// already installed for that file earlier in the same transaction disappear and
// the previously synced dataset survives untouched.
func TestShardedSourceRollsBackWhenAShardIsUnusable(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	source := config.Source{
		ID: "openbiin", Repository: "owner/openbiin", Branch: "main",
		Path: "functions/data", Format: "openbiin", Priority: 50, MinRecords: 1,
	}
	if err := db.ConfigureSources(ctx, []config.Source{source}); err != nil {
		t.Fatalf("configure sources: %v", err)
	}

	good := &shardedSourceHarness{
		commitSHA: "good-commit",
		commitAt:  time.Now().UTC(),
		dir:       "functions/data",
		shards:    map[string]string{"45.csv": openbiinShard("457100,00-99,NORDEA,DK,visa,debit")},
	}
	good.contents = map[string][]byte{
		"/repos/owner/openbiin/contents/functions/data/45.csv": []byte(good.shards["45.csv"]),
	}
	goodServer := good.start(t)
	github := NewGitHubClient("", 100<<20)
	github.baseURL = goodServer.URL
	manager := New(db.DB(), github, []config.Source{source}, 0, 0.05, 100<<20)
	if err := manager.Sync(ctx, source); err != nil {
		t.Fatalf("first sync: %v", err)
	}

	// The replacement shard set is entirely unparseable.
	bad := &shardedSourceHarness{
		commitSHA: "bad-commit",
		commitAt:  time.Now().UTC(),
		dir:       "functions/data",
		shards: map[string]string{
			"45.csv": openbiinShard("457200,00-99,REPLACEMENT BANK,DK,visa,debit"),
			"46.csv": openbiinShard("garbage,not-a-range,BROKEN,ZZ,visa,debit"),
		},
	}
	bad.contents = map[string][]byte{
		"/repos/owner/openbiin/contents/functions/data/45.csv": []byte(bad.shards["45.csv"]),
		"/repos/owner/openbiin/contents/functions/data/46.csv": []byte(bad.shards["46.csv"]),
	}
	badServer := bad.start(t)
	badGithub := NewGitHubClient("", 100<<20)
	badGithub.baseURL = badServer.URL
	badManager := New(db.DB(), badGithub, []config.Source{source}, 0, 0.05, 100<<20)

	// 45.csv parses but 46.csv contributes nothing usable; MinRecords=1 would be
	// met by the first shard alone, so the transaction is what protects the data.
	if err := badManager.Sync(ctx, source); err == nil {
		t.Fatal("sync with an unusable shard succeeded")
	}

	var rows int64
	if err := db.DB().QueryRow(`SELECT count(*) FROM bin_records WHERE source_id=?1`, source.ID).Scan(&rows); err != nil {
		t.Fatalf("count records: %v", err)
	}
	if rows != 1 {
		t.Errorf("rollback left %d records, want the 1 from the first sync", rows)
	}
	var status, sha string
	if err := db.DB().QueryRow(`SELECT status, current_sha FROM sources WHERE id=?1`, source.ID).Scan(&status, &sha); err != nil {
		t.Fatalf("read source: %v", err)
	}
	if sha != "good-commit" {
		t.Errorf("source still points at %q, want the last good commit", sha)
	}
	if status != "ready" {
		t.Errorf("source status = %q, want ready (a failed sync must not empty the dataset)", status)
	}
	result, err := db.Lookup(ctx, "45710099")
	if err != nil || result == nil {
		t.Fatalf("previously synced record no longer resolves: %v", err)
	}
}

// TestListDirectoryDistinguishesAFileFromADirectory pins the shape detection
// that lets a single-file source keep working unchanged.
func TestListDirectoryDistinguishesAFileFromADirectory(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "bins.csv") {
			// A file path: the Contents API answers with an object.
			_, _ = w.Write([]byte(`{"type":"file","path":"bins.csv","size":10}`))
			return
		}
		_, _ = w.Write([]byte(`[{"type":"file","path":"dir/00.csv","size":1},{"type":"dir","path":"dir/nested","size":0}]`))
	}))
	defer server.Close()

	github := NewGitHubClient("", 100<<20)
	github.baseURL = server.URL

	paths, isDirectory, err := github.ListDirectory(context.Background(), "owner/repo", "bins.csv", "sha")
	if err != nil {
		t.Fatalf("list single file: %v", err)
	}
	if isDirectory || paths != nil {
		t.Errorf("single file reported as directory: %v %v", paths, isDirectory)
	}

	paths, isDirectory, err = github.ListDirectory(context.Background(), "owner/repo", "dir", "sha")
	if err != nil {
		t.Fatalf("list directory: %v", err)
	}
	if !isDirectory || len(paths) != 1 || paths[0] != "dir/00.csv" {
		t.Errorf("directory listing = %v (isDirectory=%v), want only the regular file", paths, isDirectory)
	}
}

// TestDownloadAllEnforcesTotalBudget keeps a sharded source from being able to
// fill the instance's disk just because each individual shard is small.
func TestDownloadAllEnforcesTotalBudget(t *testing.T) {
	const body = "BIN6,Ranges,Issuer,Country,Brand,Type\n457100,00-99,NORDEA,DK,visa,debit\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	github := NewGitHubClient("", int64(len(body)+1))
	github.baseURL = server.URL
	paths := []string{"dir/00.csv", "dir/01.csv"}

	if _, err := github.DownloadAll(context.Background(), "owner/repo", paths, "sha"); err == nil {
		t.Fatal("combined download over MAX_DOWNLOAD_BYTES was allowed")
	} else if !strings.Contains(err.Error(), "MAX_DOWNLOAD_BYTES") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestShardedSourceRejectsEmptyDirectory(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	github := NewGitHubClient("", 100<<20)
	github.baseURL = server.URL
	if _, _, err := github.ListDirectory(context.Background(), "owner/repo", "empty", "sha"); err == nil {
		t.Fatal("an empty directory was accepted as a source")
	}
}
