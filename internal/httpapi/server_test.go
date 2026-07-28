package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/harshi79/project-17/internal/lookup"
	"github.com/harshi79/project-17/internal/model"
)

type fakeLookupStore struct{ result *model.LookupResult }

func (f fakeLookupStore) Lookup(_ context.Context, iin string) (*model.LookupResult, error) {
	if f.result == nil {
		return nil, nil
	}
	copy := *f.result
	copy.IIN = iin
	return &copy, nil
}

type fakeDataStore struct{}

func (fakeDataStore) Stats(context.Context) (model.Stats, error) {
	return model.Stats{Records: 1, Sources: []model.SourceStatus{}}, nil
}
func (fakeDataStore) Ready(context.Context) error { return nil }

func TestLookup(t *testing.T) {
	result := &model.LookupResult{
		Match:  model.Match{Start: "457173", End: "457173", Length: 6},
		Scheme: "visa", Source: model.Attribution{Commit: "abc", SyncedAt: time.Unix(1, 0)},
	}
	service := lookup.New(fakeLookupStore{result: result}, time.Minute, 10)
	handler := New(service, fakeDataStore{}, nil, "", "").Handler()

	request := httptest.NewRequest(http.MethodGet, "/v1/bin/45717360", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if response.Header().Get("ETag") == "" || response.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("missing response headers: %v", response.Header())
	}
	var got model.LookupResult
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.IIN != "45717360" || got.Scheme != "visa" {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestLookupRejectsFullPAN(t *testing.T) {
	service := lookup.New(fakeLookupStore{}, time.Minute, 10)
	handler := New(service, fakeDataStore{}, nil, "", "").Handler()
	request := httptest.NewRequest(http.MethodGet, "/v1/bin/4571736012345678", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestNotFound(t *testing.T) {
	service := lookup.New(fakeLookupStore{}, time.Minute, 10)
	handler := New(service, fakeDataStore{}, nil, "", "").Handler()
	request := httptest.NewRequest(http.MethodGet, "/123456", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
