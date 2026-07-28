package httpapi

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
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

func TestLookup(t *testing.T) {
	store := &fakeStore{result: &model.LookupResult{
		Match:  model.Match{Start: "457173", End: "457173", Length: 6},
		Scheme: "visa", Source: model.Attribution{Commit: "abc", SyncedAt: time.Unix(1, 0)},
	}}
	handler := New(store, &fakeImporter{}, "secret", 1<<20).Handler()

	request := httptest.NewRequest(http.MethodGet, "/45717360", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"iin":"45717360"`) {
		t.Fatalf("unexpected body: %s", response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("lookup must not return stale cached data: %v", response.Header())
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
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("csrf_token", server.csrfToken)
	_ = writer.WriteField("format", "binlist")
	_ = writer.WriteField("mode", "merge")
	file, err := writer.CreateFormFile("dataset", "bins.csv")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.Write([]byte("BIN,Brand\n123456,VISA\n"))
	_ = writer.Close()

	request := httptest.NewRequest(http.MethodPost, "/data/import", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
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
