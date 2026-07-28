package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/harshi79/project-17/internal/database"
	"github.com/harshi79/project-17/internal/lookup"
	"github.com/harshi79/project-17/internal/model"
	"github.com/harshi79/project-17/internal/syncer"
)

//go:embed openapi.yaml
var openAPISpec []byte

type DataStore interface {
	Stats(context.Context) (model.Stats, error)
	Ready(context.Context) error
}

type Server struct {
	lookup        *lookup.Service
	store         DataStore
	syncer        *syncer.Manager
	webhookSecret string
	adminToken    string
	startedAt     time.Time
	requests      atomic.Uint64
	errors        atomic.Uint64
}

func New(lookupService *lookup.Service, store DataStore, manager *syncer.Manager, webhookSecret, adminToken string) *Server {
	return &Server{
		lookup: lookupService, store: store, syncer: manager,
		webhookSecret: webhookSecret, adminToken: adminToken, startedAt: time.Now(),
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.home)
	mux.HandleFunc("GET /openapi.yaml", s.openAPI)
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /readyz", s.ready)
	mux.HandleFunc("GET /v1/stats", s.stats)
	mux.HandleFunc("GET /v1/bin/{iin}", s.lookupOne)
	mux.HandleFunc("POST /v1/bins:lookup", s.lookupBatch)
	mux.HandleFunc("POST /webhooks/github", s.githubWebhook)
	mux.HandleFunc("POST /internal/sync", s.manualSync)
	mux.HandleFunc("GET /metrics", s.metrics)
	// Compatibility with binlist.net's GET /{iin} endpoint.
	mux.HandleFunc("GET /{iin}", s.lookupOne)
	return s.middleware(mux)
}

func (s *Server) home(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"name": "Open BIN API", "version": "1.0.0", "rate_limit": nil,
		"lookup": "/v1/bin/{6-8 digit IIN}", "openapi": "/openapi.yaml",
	})
}

func (s *Server) openAPI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(openAPISpec)
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "uptime_seconds": int64(time.Since(s.startedAt).Seconds())})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.store.Ready(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "not_ready", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	stats, err := s.store.Stats(ctx)
	if err != nil {
		s.internalError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=60")
	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) lookupOne(w http.ResponseWriter, r *http.Request) {
	iin := r.PathValue("iin")
	if _, _, err := database.NormalizeQuery(iin); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_iin", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	result, err := s.lookup.Lookup(ctx, iin)
	if err != nil {
		s.internalError(w, err)
		return
	}
	if result == nil {
		w.Header().Set("Cache-Control", "public, max-age=60")
		writeError(w, http.StatusNotFound, "not_found", "No record covers this IIN")
		return
	}
	etag := makeETag(result)
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, max-age=300, stale-while-revalidate=86400")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

type batchRequest struct {
	BINs []string `json:"bins"`
}

type batchItem struct {
	IIN    string              `json:"iin"`
	Found  bool                `json:"found"`
	Result *model.LookupResult `json:"result"`
}

func (s *Server) lookupBatch(w http.ResponseWriter, r *http.Request) {
	var input batchRequest
	if err := decodeJSON(w, r, &input, 64<<10); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if len(input.BINs) < 1 || len(input.BINs) > 1000 {
		writeError(w, http.StatusBadRequest, "invalid_request", "bins must contain between 1 and 1000 items")
		return
	}
	for _, iin := range input.BINs {
		if _, _, err := database.NormalizeQuery(iin); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_iin", fmt.Sprintf("%q: %s", iin, err))
			return
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	items := make([]batchItem, len(input.BINs))
	semaphore := make(chan struct{}, 20)
	var wg sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex
	for index, iin := range input.BINs {
		index, iin := index, iin
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				return
			}
			result, err := s.lookup.Lookup(ctx, iin)
			if err != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				errMu.Unlock()
				cancel()
				return
			}
			items[index] = batchItem{IIN: iin, Found: result != nil, Result: result}
		}()
	}
	wg.Wait()
	if firstErr != nil {
		s.internalError(w, firstErr)
		return
	}
	if err := ctx.Err(); err != nil {
		s.internalError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"results": items})
}

func (s *Server) githubWebhook(w http.ResponseWriter, r *http.Request) {
	if s.syncer == nil || s.webhookSecret == "" {
		writeError(w, http.StatusServiceUnavailable, "webhook_not_configured", "GitHub webhook sync is not configured")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 2<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Webhook body is too large or unreadable")
		return
	}
	if !verifyGitHubSignature(body, r.Header.Get("X-Hub-Signature-256"), s.webhookSecret) {
		writeError(w, http.StatusUnauthorized, "invalid_signature", "Invalid GitHub webhook signature")
		return
	}
	switch r.Header.Get("X-GitHub-Event") {
	case "ping":
		writeJSON(w, http.StatusOK, map[string]string{"status": "pong"})
		return
	case "push":
	default:
		writeJSON(w, http.StatusAccepted, map[string]any{"queued": []string{}})
		return
	}
	var event struct {
		Ref        string `json:"ref"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
		Commits []struct {
			Added    []string `json:"added"`
			Modified []string `json:"modified"`
			Removed  []string `json:"removed"`
		} `json:"commits"`
	}
	if err := json.Unmarshal(body, &event); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid webhook JSON")
		return
	}
	changed := make(map[string]bool)
	for _, commit := range event.Commits {
		for _, file := range append(append(commit.Added, commit.Modified...), commit.Removed...) {
			changed[file] = true
		}
	}
	ids := s.syncer.SourcesForRepository(event.Repository.FullName, event.Ref, changed)
	queued := make([]string, 0, len(ids))
	for _, id := range ids {
		if s.syncer.Trigger(id) {
			queued = append(queued, id)
		}
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"queued": queued})
}

func (s *Server) manualSync(w http.ResponseWriter, r *http.Request) {
	if s.syncer == nil || s.adminToken == "" {
		writeError(w, http.StatusNotFound, "not_found", "Not found")
		return
	}
	provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if subtle.ConstantTimeCompare([]byte(provided), []byte(s.adminToken)) != 1 {
		writeError(w, http.StatusUnauthorized, "unauthorized", "A valid bearer token is required")
		return
	}
	var input struct {
		Source string `json:"source"`
	}
	if err := decodeJSON(w, r, &input, 4<<10); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if input.Source == "" {
		s.syncer.TriggerAll()
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "all sources queued"})
		return
	}
	if !s.syncer.Trigger(input.Source) {
		writeError(w, http.StatusNotFound, "unknown_source", "Unknown source id")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "source queued", "source": input.Source})
}

func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	hits, misses, entries := s.lookup.Metrics()
	stats, _ := s.store.Stats(r.Context())
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = fmt.Fprintf(w, "# HELP open_bin_http_requests_total HTTP requests handled.\n# TYPE open_bin_http_requests_total counter\nopen_bin_http_requests_total %d\n", s.requests.Load())
	_, _ = fmt.Fprintf(w, "# HELP open_bin_http_errors_total HTTP 5xx responses.\n# TYPE open_bin_http_errors_total counter\nopen_bin_http_errors_total %d\n", s.errors.Load())
	_, _ = fmt.Fprintf(w, "# TYPE open_bin_cache_hits_total counter\nopen_bin_cache_hits_total %d\n# TYPE open_bin_cache_misses_total counter\nopen_bin_cache_misses_total %d\n", hits, misses)
	_, _ = fmt.Fprintf(w, "# TYPE open_bin_cache_entries gauge\nopen_bin_cache_entries %d\n# TYPE open_bin_records gauge\nopen_bin_records %d\n", entries, stats.Records)
}

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		s.requests.Add(1)
		requestID := r.Header.Get("X-Request-ID")
		if requestID == "" || len(requestID) > 128 {
			requestID = strconv.FormatUint(s.requests.Load(), 36) + "-" + strconv.FormatInt(started.UnixNano(), 36)
		}
		w.Header().Set("X-Request-ID", requestID)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, If-None-Match, X-Request-ID, X-Hub-Signature-256, X-GitHub-Event")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		wrapped := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		defer func() {
			if recovered := recover(); recovered != nil {
				s.errors.Add(1)
				slog.Error("panic recovered", "request_id", requestID, "error", recovered)
				if !wrapped.wroteHeader {
					writeError(wrapped, http.StatusInternalServerError, "internal_error", "An internal error occurred")
				}
			}
			slog.Info("http request", "request_id", requestID, "method", r.Method,
				"status", wrapped.status, "duration_ms", time.Since(started).Milliseconds())
		}()
		next.ServeHTTP(wrapped, r)
	})
}

func (s *Server) internalError(w http.ResponseWriter, err error) {
	s.errors.Add(1)
	slog.Error("request failed", "error", err)
	writeError(w, http.StatusInternalServerError, "internal_error", "An internal error occurred")
}

type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.status = status
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(body []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func verifyGitHubSignature(body []byte, signature, secret string) bool {
	if !strings.HasPrefix(signature, "sha256=") {
		return false
	}
	provided, err := hex.DecodeString(strings.TrimPrefix(signature, "sha256="))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return hmac.Equal(provided, mac.Sum(nil))
}

func makeETag(result *model.LookupResult) string {
	sum := sha256.Sum256([]byte(result.IIN + "\x00" + result.Match.Start + "\x00" + result.Match.End + "\x00" + result.Source.Commit))
	return `"` + hex.EncodeToString(sum[:12]) + `"`
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any, limit int64) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON value")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
