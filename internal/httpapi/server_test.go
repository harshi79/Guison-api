package httpapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/harshi79/project-17/internal/model"
)

type fakeStore struct {
	result *model.LookupResult
	stats  model.Stats
}

func (f *fakeStore) Lookup(_ context.Context, iin string) (*model.LookupResult, error) {
	if f.result == nil {
		return nil, nil
	}
	copy := *f.result
	copy.IIN = iin
	return &copy, nil
}
func (f *fakeStore) Stats(context.Context) (model.Stats, error) { return f.stats, nil }
func (f *fakeStore) Ready(context.Context) error                { return nil }

type panicStore struct{}

func (*panicStore) Lookup(context.Context, string) (*model.LookupResult, error) {
	panic("health endpoint queried lookup storage")
}
func (*panicStore) Stats(context.Context) (model.Stats, error) {
	panic("health endpoint queried stats storage")
}
func (*panicStore) Ready(context.Context) error {
	panic("health endpoint queried readiness storage")
}

type fakeImporter struct {
	called  bool
	format  string
	replace bool
	content string
}

func (f *fakeImporter) CheckAll(context.Context) error { f.called = true; return nil }
func (f *fakeImporter) ImportUpload(_ context.Context, reader io.Reader, _ string, format string, replace bool) (int64, error) {
	data, _ := io.ReadAll(reader)
	f.called, f.format, f.replace, f.content = true, format, replace, string(data)
	return 3, nil
}

func TestUptimeHealthDoesNotUseDependencies(t *testing.T) {
	handler := New(&panicStore{}, &fakeImporter{}, "secret", 1<<20).Handler()
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if response.Body.String() != "OK\n" {
		t.Fatalf("body=%q, want OK", response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
		t.Fatalf("Content-Type=%q", got)
	}
}

func TestLookup(t *testing.T) {
	store := &fakeStore{result: &model.LookupResult{
		Match:  model.Match{Start: "457173", End: "457173", Length: 6},
		Scheme: "visa", Source: model.Attribution{Commit: "abc", SyncedAt: time.Unix(1, 0)},
	}}
	handler := New(store, &fakeImporter{}, "secret", 1<<20).Handler()

	for _, path := range []string{"/45717360", "/v1/bin/45717360"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("path=%s status=%d body=%s", path, response.Code, response.Body.String())
		}
		if !strings.Contains(response.Body.String(), `"iin":"45717360"`) {
			t.Fatalf("path=%s unexpected body: %s", path, response.Body.String())
		}
		if response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("path=%s lookup must not return stale cached data: %v", path, response.Header())
		}
	}
}

func TestLookupNotFound(t *testing.T) {
	handler := New(&fakeStore{}, &fakeImporter{}, "secret", 1<<20).Handler()
	request := httptest.NewRequest(http.MethodGet, "/123456", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestLookupRejectsFullPAN(t *testing.T) {
	handler := New(&fakeStore{}, &fakeImporter{}, "secret", 1<<20).Handler()
	request := httptest.NewRequest(http.MethodGet, "/4571736012345678", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestDataPageRequiresServerSidePassword(t *testing.T) {
	server := New(&fakeStore{}, &fakeImporter{}, "very-secret-password", 1<<20)
	handler := server.Handler()

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/data", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d", unauthorized.Code)
	}
	if got := unauthorized.Header().Get("WWW-Authenticate"); got != `Basic realm="BIN data admin", charset="UTF-8"` {
		t.Fatalf("unexpected authentication challenge %q", got)
	}

	request := httptest.NewRequest(http.MethodGet, "/data", nil)
	request.SetBasicAuth("admin", "very-secret-password")
	authorized := httptest.NewRecorder()
	handler.ServeHTTP(authorized, request)
	if authorized.Code != http.StatusOK {
		t.Fatalf("authorized status=%d body=%s", authorized.Code, authorized.Body.String())
	}
	if strings.Contains(authorized.Body.String(), "very-secret-password") {
		t.Fatal("admin password leaked into HTML")
	}
}

func TestDataImport(t *testing.T) {
	dataImporter := &fakeImporter{}
	server := New(&fakeStore{}, dataImporter, "secret", 1<<20)
	request := newUploadRequest(t, server.csrfToken, "merge")
	request.SetBasicAuth("admin", "secret")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if !dataImporter.called || dataImporter.format != "binlist" || dataImporter.replace || !strings.Contains(dataImporter.content, "123456") {
		t.Fatalf("unexpected import call: %+v", dataImporter)
	}
}

func TestDataReplace(t *testing.T) {
	dataImporter := &fakeImporter{}
	server := New(&fakeStore{}, dataImporter, "secret", 1<<20)
	request := newUploadRequest(t, server.csrfToken, "replace")
	request.SetBasicAuth("admin", "secret")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || !dataImporter.called || !dataImporter.replace {
		t.Fatalf("replace import failed: status=%d importer=%+v body=%s", response.Code, dataImporter, response.Body.String())
	}
}

func TestDataImportRejectsInvalidCSRF(t *testing.T) {
	dataImporter := &fakeImporter{}
	server := New(&fakeStore{}, dataImporter, "secret", 1<<20)
	request := newUploadRequest(t, "wrong-token", "merge")
	request.SetBasicAuth("admin", "secret")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if dataImporter.called {
		t.Fatal("importer was called after failed CSRF validation")
	}
}

func TestDataImportRejectsInvalidMode(t *testing.T) {
	dataImporter := &fakeImporter{}
	server := New(&fakeStore{}, dataImporter, "secret", 1<<20)
	request := newUploadRequest(t, server.csrfToken, "unknown")
	request.SetBasicAuth("admin", "secret")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if dataImporter.called {
		t.Fatal("importer was called with an invalid mode")
	}
}

func TestDataUpdate(t *testing.T) {
	dataImporter := &fakeImporter{}
	server := New(&fakeStore{}, dataImporter, "secret", 1<<20)
	form := url.Values{"csrf_token": []string{server.csrfToken}}
	request := httptest.NewRequest(http.MethodPost, "/data/update", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.SetBasicAuth("admin", "secret")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || !dataImporter.called {
		t.Fatalf("update check failed: status=%d importer=%+v body=%s", response.Code, dataImporter, response.Body.String())
	}
}

func newUploadRequest(t *testing.T, csrfToken, mode string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("csrf_token", csrfToken)
	_ = writer.WriteField("format", "binlist")
	_ = writer.WriteField("mode", mode)
	file, err := writer.CreateFormFile("dataset", "bins.csv")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.Write([]byte("BIN,Brand\n123456,VISA\n"))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/data/import", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}

// errorStore fails every query so page degradation can be exercised.
type errorStore struct{}

func (*errorStore) Lookup(context.Context, string) (*model.LookupResult, error) {
	return nil, errors.New("database unavailable")
}
func (*errorStore) Stats(context.Context) (model.Stats, error) {
	return model.Stats{}, errors.New("database unavailable")
}
func (*errorStore) Ready(context.Context) error { return errors.New("database unavailable") }
