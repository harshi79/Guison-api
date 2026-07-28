package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/harshi79/project-17/internal/model"
)

func newSiteServer() http.Handler {
	store := &fakeStore{stats: model.Stats{
		Records: 374788,
		Sources: []model.SourceStatus{{
			ID: "bin-list-data", Repository: "venelinkochev/bin-list-data",
			Priority: 100, Status: "ready", RecordCount: 374788,
		}},
	}}
	return New(store, &fakeImporter{}, "very-secret-password", 1<<20).Handler()
}

func TestHomePageServesHTML(t *testing.T) {
	response := httptest.NewRecorder()
	newSiteServer().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d", response.Code)
	}
	if got := response.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Fatalf("Content-Type=%q, want text/html", got)
	}
	body := response.Body.String()
	for _, want := range []string{
		"<!doctype html>", "Guison", "Try API", "Read Docs",
		`id="tester"`, `id="features"`, `id="faq"`, `id="community"`,
		"/assets/site.css", "/assets/site.js",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("homepage missing %q", want)
		}
	}
}

func TestHomePageShowsLiveRecordCount(t *testing.T) {
	response := httptest.NewRecorder()
	newSiteServer().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))

	// html/template escapes "+" as "&#43;" in text context.
	body := response.Body.String()
	if !strings.Contains(body, "374K&#43;") && !strings.Contains(body, "374K+") {
		t.Error("homepage should render the live record count from Stats")
	}
	if !strings.Contains(body, "BIN records") {
		t.Error("record count should be labelled")
	}
}

// A failing datastore must not take the marketing site down.
func TestHomePageDegradesWithoutStats(t *testing.T) {
	server := New(&errorStore{}, &fakeImporter{}, "very-secret-password", 1<<20)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200 even when stats fail", response.Code)
	}
	body := response.Body.String()
	if !strings.Contains(body, "Guison") {
		t.Error("page should still render")
	}
	if strings.Contains(body, "K&#43;") || strings.Contains(body, "BIN records") {
		t.Error("no record count should be claimed when stats are unavailable")
	}
	if !strings.Contains(body, "BIN dataset") {
		t.Error("expected neutral fallback wording instead of a fabricated count")
	}
}

func TestDocsPageServesHTML(t *testing.T) {
	response := httptest.NewRecorder()
	newSiteServer().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/docs", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d", response.Code)
	}
	if got := response.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Fatalf("Content-Type=%q, want text/html", got)
	}
	body := response.Body.String()
	for _, want := range []string{
		"Overview", "Base URL", "Quick Start", "Authentication",
		"Endpoints", "Response schema", "Field descriptions",
		"Error responses", "Health endpoints", "Data freshness",
		"Source attribution", "invalid_iin", "not_found", "internal_error",
		"/v1/bin/", "CC BY 4.0",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("docs missing %q", want)
		}
	}
}

// /docs and /assets must never be treated as BIN lookup input.
func TestDocsIsNotTreatedAsLookup(t *testing.T) {
	response := httptest.NewRecorder()
	newSiteServer().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/docs", nil))

	body := response.Body.String()
	if strings.HasPrefix(strings.TrimSpace(body), "{") {
		t.Fatal("/docs was routed into the BIN lookup handler")
	}
	if got := response.Header().Get("Content-Type"); strings.HasPrefix(got, "application/json") {
		t.Fatalf("/docs returned %q, so it hit the lookup route", got)
	}
	// The same must hold for the asset prefix.
	assets := httptest.NewRecorder()
	newSiteServer().ServeHTTP(assets, httptest.NewRequest(http.MethodGet, "/assets/site.css", nil))
	if strings.Contains(assets.Body.String(), "invalid_iin") {
		t.Fatal("/assets/ was routed into the BIN lookup handler")
	}
}

func TestStaticAssetsAreServed(t *testing.T) {
	handler := newSiteServer()
	for path, contentType := range map[string]string{
		"/assets/site.css":      "text/css",
		"/assets/site.js":       "javascript",
		"/assets/art/hero.webp": "image/webp",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Errorf("%s status=%d", path, response.Code)
			continue
		}
		if got := response.Header().Get("Content-Type"); !strings.Contains(got, contentType) {
			t.Errorf("%s Content-Type=%q, want %q", path, got, contentType)
		}
		if response.Body.Len() == 0 {
			t.Errorf("%s served an empty body", path)
		}
	}
}

// Existing API behaviour must be unchanged by the website.
func TestLookupRoutesStillWorkAlongsideSite(t *testing.T) {
	store := &fakeStore{result: &model.LookupResult{
		Match: model.Match{Start: "457173", End: "457173", Length: 6}, Scheme: "visa",
	}}
	handler := New(store, &fakeImporter{}, "very-secret-password", 1<<20).Handler()

	for _, path := range []string{"/45717360", "/v1/bin/45717360", "/457173"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s status=%d", path, response.Code)
		}
		if got := response.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
			t.Fatalf("%s must stay JSON, got %q", path, got)
		}
	}
}

func TestHealthRoutesStillWork(t *testing.T) {
	handler := newSiteServer()
	for path, wantStatus := range map[string]int{
		"/health": http.StatusOK, "/healthz": http.StatusOK, "/readyz": http.StatusOK,
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != wantStatus {
			t.Errorf("%s status=%d, want %d", path, response.Code, wantStatus)
		}
	}
}

func TestDataPageStaysProtected(t *testing.T) {
	response := httptest.NewRecorder()
	newSiteServer().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/data", nil))

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("/data status=%d, want 401", response.Code)
	}
}

// The public site must not advertise administrative credentials.
func TestSiteDoesNotLeakAdminDetails(t *testing.T) {
	handler := newSiteServer()
	for _, path := range []string{"/", "/docs"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		body := response.Body.String()
		for _, secret := range []string{
			"very-secret-password", "DATA_ADMIN_PASSWORD", "DATABASE_URL", "postgres://",
		} {
			if strings.Contains(body, secret) {
				t.Errorf("%s leaked %q", path, secret)
			}
		}
	}
}

// Telegram links must never ship as invented usernames.
func TestTelegramLinksArePlaceholdersUntilConfigured(t *testing.T) {
	t.Setenv(envTelegramChannel, "")
	t.Setenv(envTelegramPrivate, "")
	t.Setenv(envTelegramDeveloper, "")

	response := httptest.NewRecorder()
	newSiteServer().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	body := response.Body.String()

	if strings.Contains(body, "t.me/") {
		t.Error("unconfigured build must not contain any t.me link")
	}
	for _, name := range []string{"TELEGRAM_CHANNEL_URL", "TELEGRAM_PRIVATE_URL", "DEVELOPER_TELEGRAM_URL"} {
		if !strings.Contains(body, name) {
			t.Errorf("expected a visible setup hint naming %s", name)
		}
	}
	// All three buttons must render disabled rather than as dead links.
	if got := strings.Count(body, `disabled aria-disabled="true"`); got != 3 {
		t.Errorf("disabled buttons=%d, want 3", got)
	}
	for _, label := range []string{"Official Channel", "Private Community", "Contact Developer"} {
		if !strings.Contains(body, label) {
			t.Errorf("missing community label %q", label)
		}
	}
}

func TestTelegramLinksRenderWhenConfigured(t *testing.T) {
	t.Setenv(envTelegramChannel, "https://t.me/yorifederation")
	t.Setenv(envTelegramPrivate, "https://t.me/+y8EekRvqpnQzNjZl")
	t.Setenv(envTelegramDeveloper, "https://t.me/YorichiiPrime")

	response := httptest.NewRecorder()
	newSiteServer().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	body := response.Body.String()

	for _, want := range []string{
		`href="https://t.me/yorifederation"`,
		`rel="noopener noreferrer external"`,
		"Official Channel", "Private Community", "Contact Developer",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("configured page missing %q", want)
		}
	}
	// The private invite must be reachable but never shown as page copy.
	if !strings.Contains(body, "y8EekRvqpnQzNjZl") {
		t.Error("private invite should be present as a link target")
	}
	if strings.Contains(body, ">https://t.me/") || strings.Contains(body, "> https://t.me/") {
		t.Error("raw Telegram URLs must not be rendered as visible text")
	}
	// No setup hint should remain once everything is configured.
	if strings.Contains(body, "Setup required") {
		t.Error("setup hint should disappear when all links are configured")
	}
	if strings.Contains(body, `disabled aria-disabled="true"`) {
		t.Error("no community button should be disabled once configured")
	}
}

// Only http(s) destinations are accepted, so a hostile value cannot inject a scheme.
func TestCommunityLinkRejectsUnsafeSchemes(t *testing.T) {
	for _, value := range []string{
		"javascript:alert(1)", "data:text/html,x", "tg://resolve",
		"  ", "//evil.example.com", "vbscript:msgbox(1)",
	} {
		t.Setenv(envTelegramPrivate, value)
		link := communityLink(envTelegramPrivate, "Private Community", "btn-ghost")
		if link.Configured {
			t.Errorf("value %q must not be accepted as a link", value)
		}
		if link.URL != "" {
			t.Errorf("value %q must not populate a URL", value)
		}
	}
}

func TestHumaniseCount(t *testing.T) {
	cases := map[int64]string{0: "0", 42: "42", 999: "999", 1000: "1K+", 374788: "374K+", 2500000: "2.5M+"}
	for input, want := range cases {
		if got := humaniseCount(input); got != want {
			t.Errorf("humaniseCount(%d)=%q, want %q", input, got, want)
		}
	}
}

func TestPublicBaseURL(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Host = "guison-api.onrender.com"
	request.Header.Set("X-Forwarded-Proto", "https")
	if got := publicBaseURL(request); got != "https://guison-api.onrender.com" {
		t.Fatalf("baseURL=%q", got)
	}

	local := httptest.NewRequest(http.MethodGet, "/", nil)
	local.Host = "localhost:8080"
	if got := publicBaseURL(local); got != "http://localhost:8080" {
		t.Fatalf("local baseURL=%q", got)
	}
}

// --- Public response must not leak internal source provenance ---

// lookupResultWithSource mirrors a real row: internal provenance is populated
// by the database layer on every lookup.
func lookupResultWithSource() *model.LookupResult {
	synced, _ := time.Parse(time.RFC3339, "2025-02-11T13:40:06Z")
	length := int16(16)
	luhn := true
	return &model.LookupResult{
		Match:  model.Match{Start: "457173", End: "457173", Length: 6},
		Number: model.Number{Length: &length, Luhn: &luhn},
		Scheme: "visa", Brand: "Visa/Dankort", Type: "debit",
		Country: model.Country{Alpha2: "DK", Alpha3: "DNK", Name: "Denmark", Currency: "DKK"},
		Bank:    model.Bank{Name: "Jyske Bank", URL: "www.jyskebank.dk"},
		Source: model.Attribution{
			ID: "bin-list-data", Repository: "venelinkochev/bin-list-data",
			Commit: "023a4f6c1d", SyncedAt: synced,
		},
	}
}

func TestPublicLookupOmitsSourceObject(t *testing.T) {
	handler := New(&fakeStore{result: lookupResultWithSource()}, &fakeImporter{}, "very-secret-password", 1<<20).Handler()

	for _, path := range []string{"/45717360", "/v1/bin/45717360"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s status=%d", path, response.Code)
		}

		var body map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s invalid JSON: %v", path, err)
		}
		if _, present := body["source"]; present {
			t.Errorf("%s still returns a source object", path)
		}

		// No replacement provenance key may appear either.
		for _, banned := range []string{"repository", "commit", "synced_at", "attribution", "provenance", "dataset"} {
			if _, present := body[banned]; present {
				t.Errorf("%s exposes provenance key %q", path, banned)
			}
		}

		// And nothing internal may leak anywhere in the raw payload.
		raw := response.Body.String()
		for _, secret := range []string{
			"venelinkochev", "bin-list-data", "023a4f6c1d", "2025-02-11T13:40:06Z", "synced_at",
		} {
			if strings.Contains(raw, secret) {
				t.Errorf("%s leaked internal value %q: %s", path, secret, raw)
			}
		}
	}
}

// Removing source must not disturb the rest of the documented payload.
func TestPublicLookupKeepsCardFields(t *testing.T) {
	handler := New(&fakeStore{result: lookupResultWithSource()}, &fakeImporter{}, "very-secret-password", 1<<20).Handler()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/45717360", nil))

	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	for _, want := range []string{"iin", "match", "number", "scheme", "brand", "type", "level", "prepaid", "country", "bank"} {
		if _, present := body[want]; !present {
			t.Errorf("public response lost field %q", want)
		}
	}
	if len(body) != 10 {
		t.Errorf("public response has %d top-level fields, want exactly 10: %v", len(body), body)
	}
	if body["iin"] != "45717360" {
		t.Errorf("iin=%v", body["iin"])
	}
}

// Internal provenance must survive in the Go model even though it is not serialised.
func TestInternalSourceTrackingStillPopulated(t *testing.T) {
	store := &fakeStore{result: lookupResultWithSource()}
	result, err := store.Lookup(context.Background(), "45717360")
	if err != nil || result == nil {
		t.Fatalf("lookup failed: %v", err)
	}
	if result.Source.ID != "bin-list-data" ||
		result.Source.Repository != "venelinkochev/bin-list-data" ||
		result.Source.Commit != "023a4f6c1d" ||
		result.Source.SyncedAt.IsZero() {
		t.Fatalf("internal source tracking was lost: %+v", result.Source)
	}
}

// The admin page must still surface source status.
func TestAdminPageStillShowsSourceStatus(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/data", nil)
	request.SetBasicAuth("admin", "very-secret-password")
	response := httptest.NewRecorder()
	newSiteServer().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d", response.Code)
	}
	body := response.Body.String()
	for _, want := range []string{"bin-list-data", "venelinkochev/bin-list-data", "374788"} {
		if !strings.Contains(body, want) {
			t.Errorf("/data lost source status %q", want)
		}
	}
}

// SourceStatus is an internal type and must keep its JSON tags for /data.
func TestSourceStatusStillSerialises(t *testing.T) {
	encoded, err := json.Marshal(model.SourceStatus{
		ID: "bin-list-data", Repository: "venelinkochev/bin-list-data", CurrentCommit: "023a4f6",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"id"`, `"repository"`, `"current_commit"`} {
		if !strings.Contains(string(encoded), want) {
			t.Errorf("SourceStatus lost %s", want)
		}
	}
}

// The public site must not document a field the API no longer returns.
func TestSiteDoesNotDocumentSourceField(t *testing.T) {
	handler := newSiteServer()
	for _, path := range []string{"/", "/docs"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		body := response.Body.String()

		for _, banned := range []string{
			`"source"`, "source.id", "source.repository", "source.commit", "source.synced_at",
		} {
			if strings.Contains(body, banned) {
				t.Errorf("%s still documents %q", path, banned)
			}
		}
	}
}

// Dataset credit is deliberately kept, only per-response provenance is removed.
func TestSiteKeepsDatasetCredit(t *testing.T) {
	handler := newSiteServer()
	for _, path := range []string{"/", "/docs"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		body := response.Body.String()
		for _, want := range []string{"venelinkochev/bin-list-data", "CC BY 4.0"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s dropped dataset credit %q", path, want)
			}
		}
	}
}

// The tester script must no longer reference the removed field.
func TestTesterScriptHasNoSourceHandling(t *testing.T) {
	response := httptest.NewRecorder()
	newSiteServer().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/assets/site.js", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d", response.Code)
	}
	script := response.Body.String()
	for _, banned := range []string{"data.source", "source.repository", "source.id"} {
		if strings.Contains(script, banned) {
			t.Errorf("tester still reads %q", banned)
		}
	}
}
