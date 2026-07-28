package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/harshi79/project-17/internal/database"
	"github.com/harshi79/project-17/internal/model"
)

type DataStore interface {
	Lookup(context.Context, string) (*model.LookupResult, error)
	Stats(context.Context) (model.Stats, error)
	Ready(context.Context) error
}

type DataImporter interface {
	CheckAll(context.Context) error
	ImportUpload(context.Context, io.Reader, string, string, bool) (int64, error)
}

type Server struct {
	store         DataStore
	importer      DataImporter
	adminPassword string
	csrfToken     string
	maxUpload     int64
	startedAt     time.Time
	requests      atomic.Uint64
	dataTemplate  *template.Template
}

func New(store DataStore, dataImporter DataImporter, adminPassword string, maxUpload int64) *Server {
	return &Server{
		store: store, importer: dataImporter, adminPassword: adminPassword,
		csrfToken: randomToken(), maxUpload: maxUpload, startedAt: time.Now(),
		dataTemplate: template.Must(template.New("data").Parse(dataPageHTML)),
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.home)
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /readyz", s.ready)
	mux.HandleFunc("GET /data", s.dataPage)
	mux.HandleFunc("POST /data/import", s.dataImport)
	mux.HandleFunc("POST /data/update", s.dataUpdate)
	mux.HandleFunc("GET /v1/bin/{iin}", s.lookup)
	mux.HandleFunc("GET /{iin}", s.lookup)
	return s.middleware(mux)
}

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name": "Open BIN API", "lookup": "/{6-8 digit BIN}", "admin": "/data",
	})
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

func (s *Server) lookup(w http.ResponseWriter, r *http.Request) {
	iin := r.PathValue("iin")
	if _, _, err := database.NormalizeQuery(iin); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_iin", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	result, err := s.store.Lookup(ctx, iin)
	if err != nil {
		s.internalError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if result == nil {
		writeError(w, http.StatusNotFound, "not_found", "No record covers this BIN/IIN")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) dataPage(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	s.renderDataPage(w, r, http.StatusOK, r.URL.Query().Get("message"), "")
}

func (s *Server) dataImport(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.maxUpload+(1<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		s.renderDataPage(w, r, http.StatusBadRequest, "", "The upload is too large or the form is invalid.")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	if !s.validCSRF(r.FormValue("csrf_token")) {
		s.renderDataPage(w, r, http.StatusForbidden, "", "The form expired. Reload /data and try again.")
		return
	}

	file, header, err := r.FormFile("dataset")
	if err != nil {
		s.renderDataPage(w, r, http.StatusBadRequest, "", "Choose a CSV file to import.")
		return
	}
	defer file.Close()
	format := r.FormValue("format")
	replace := r.FormValue("mode") == "replace"
	records, err := s.importer.ImportUpload(r.Context(), file, header.Filename, format, replace)
	if err != nil {
		s.renderDataPage(w, r, http.StatusBadRequest, "", "Import failed: "+err.Error())
		return
	}
	message := fmt.Sprintf("Import complete. The manual dataset now contains %d records.", records)
	http.Redirect(w, r, "/data?message="+url.QueryEscape(message), http.StatusSeeOther)
}

func (s *Server) dataUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := r.ParseForm(); err != nil {
		s.renderDataPage(w, r, http.StatusBadRequest, "", "Invalid form submission.")
		return
	}
	if !s.validCSRF(r.FormValue("csrf_token")) {
		s.renderDataPage(w, r, http.StatusForbidden, "", "The form expired. Reload /data and try again.")
		return
	}
	if err := s.importer.CheckAll(r.Context()); err != nil {
		s.renderDataPage(w, r, http.StatusBadGateway, "", "Automatic source check failed: "+err.Error())
		return
	}
	http.Redirect(w, r, "/data?message="+url.QueryEscape("Configured sources are up to date."), http.StatusSeeOther)
}

func (s *Server) renderDataPage(w http.ResponseWriter, r *http.Request, status int, message, errorMessage string) {
	setAdminHeaders(w)
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	stats, err := s.store.Stats(ctx)
	if err != nil {
		http.Error(w, "Could not read dataset status", http.StatusInternalServerError)
		return
	}
	view := struct {
		CSRF    string
		Message string
		Error   string
		Stats   model.Stats
	}{s.csrfToken, message, errorMessage, stats}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := s.dataTemplate.Execute(w, view); err != nil {
		slog.Error("render data page", "error", err)
	}
}

func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	setAdminHeaders(w)
	if s.adminPassword == "" {
		http.Error(w, "Set DATA_ADMIN_PASSWORD to enable this page.", http.StatusServiceUnavailable)
		return false
	}
	username, password, ok := r.BasicAuth()
	providedPassword := sha256.Sum256([]byte(password))
	expectedPassword := sha256.Sum256([]byte(s.adminPassword))
	validUser := subtle.ConstantTimeCompare([]byte(username), []byte("admin")) == 1
	validPassword := subtle.ConstantTimeCompare(providedPassword[:], expectedPassword[:]) == 1
	if !ok || !validUser || !validPassword {
		w.Header().Set("WWW-Authenticate", `Basic realm="BIN data admin", charset="UTF-8"`)
		http.Error(w, "Authentication required", http.StatusUnauthorized)
		return false
	}
	return true
}

func (s *Server) validCSRF(provided string) bool {
	return subtle.ConstantTimeCompare([]byte(provided), []byte(s.csrfToken)) == 1
}

func setAdminHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

func randomToken() string {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		panic("could not create CSRF token: " + err.Error())
	}
	return hex.EncodeToString(value)
}

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		requestNumber := s.requests.Add(1)
		requestID := r.Header.Get("X-Request-ID")
		if requestID == "" || len(requestID) > 128 {
			requestID = strconv.FormatUint(requestNumber, 36) + "-" + strconv.FormatInt(started.UnixNano(), 36)
		}
		w.Header().Set("X-Request-ID", requestID)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if !strings.HasPrefix(r.URL.Path, "/data") {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "X-Request-ID")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		wrapped := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		defer func() {
			if recovered := recover(); recovered != nil {
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

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

const dataPageHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width,initial-scale=1">
  <title>BIN data</title>
  <style>
    body{font:16px/1.5 system-ui,sans-serif;max-width:960px;margin:2rem auto;padding:0 1rem;color:#17202a;background:#f7f8fa}
    h1,h2{line-height:1.2}section{background:white;border:1px solid #dde2e7;border-radius:10px;padding:1.25rem;margin:1rem 0}
    label{display:block;font-weight:600;margin:.8rem 0 .3rem}input,select,button{font:inherit}input[type=file],select{width:100%;max-width:520px;padding:.55rem}
    button{margin-top:1rem;padding:.65rem 1rem;border:0;border-radius:6px;background:#1769aa;color:white;cursor:pointer}.muted{color:#59636e}
    .ok,.error{padding:.8rem;border-radius:6px}.ok{background:#e8f6ec;color:#175c2c}.error{background:#fdecec;color:#8a1f1f}
    table{width:100%;border-collapse:collapse;font-size:.9rem}th,td{text-align:left;padding:.55rem;border-bottom:1px solid #e5e8eb;vertical-align:top;word-break:break-word}
    code{font-size:.85em}fieldset{border:0;padding:0;margin:0}label.inline{display:inline;font-weight:400;margin-right:1rem}
  </style>
</head>
<body>
  <h1>BIN data</h1>
  <p class="muted">Import CSV data and check configured GitHub datasets. Existing data stays live until a complete import succeeds.</p>
  {{if .Message}}<p class="ok">{{.Message}}</p>{{end}}
  {{if .Error}}<p class="error">{{.Error}}</p>{{end}}

  <section>
    <h2>Import CSV</h2>
    <form method="post" action="/data/import" enctype="multipart/form-data">
      <input type="hidden" name="csrf_token" value="{{.CSRF}}">
      <label for="dataset">CSV file</label>
      <input id="dataset" name="dataset" type="file" accept=".csv,text/csv" required>
      <label for="format">CSV format</label>
      <select id="format" name="format">
        <option value="binlist">BIN list (BIN, Brand, Type, Category, Issuer...)</option>
        <option value="ranges">Ranges (iin_start, iin_end, scheme, bank_name...)</option>
        <option value="generic">Generic header aliases</option>
      </select>
      <label>Import mode</label>
      <fieldset>
        <label class="inline"><input type="radio" name="mode" value="merge" checked> Merge with manual data</label>
        <label class="inline"><input type="radio" name="mode" value="replace"> Replace manual data</label>
      </fieldset>
      <button type="submit">Import data</button>
    </form>
  </section>

  <section>
    <h2>Automatic sources</h2>
    <p class="muted">The server checks these sources periodically. Use this button to check them now.</p>
    <form method="post" action="/data/update">
      <input type="hidden" name="csrf_token" value="{{.CSRF}}">
      <button type="submit">Check for updates</button>
    </form>
  </section>

  <section>
    <h2>Status</h2>
    <p><strong>{{.Stats.Records}}</strong> imported records across active sources.</p>
    <table>
      <thead><tr><th>Source</th><th>Status</th><th>Records</th><th>Last import</th><th>Details</th></tr></thead>
      <tbody>
      {{range .Stats.Sources}}
        <tr>
          <td><strong>{{.ID}}</strong><br><span class="muted">{{.Repository}} / {{.Path}}</span></td>
          <td>{{.Status}}</td><td>{{.RecordCount}}</td><td>{{if .LastSyncedAt}}{{.LastSyncedAt}}{{else}}Never{{end}}</td>
          <td>{{if .LastError}}<span class="error">{{.LastError}}</span>{{else if .CurrentCommit}}<code>{{.CurrentCommit}}</code>{{else}}—{{end}}</td>
        </tr>
      {{else}}<tr><td colspan="5">No sources configured.</td></tr>{{end}}
      </tbody>
    </table>
  </section>
  <p class="muted">This page uses HTTP Basic authentication with username <code>admin</code>. Serve it over HTTPS.</p>
</body>
</html>`
