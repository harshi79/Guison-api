package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	t.Setenv(envTelegramDeveloper, "")

	response := httptest.NewRecorder()
	newSiteServer().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	body := response.Body.String()

	if strings.Contains(body, "t.me/") {
		t.Error("unconfigured build must not contain any t.me link")
	}
	if !strings.Contains(body, "TELEGRAM_CHANNEL_URL") {
		t.Error("expected a visible setup hint naming the environment variable")
	}
	if !strings.Contains(body, "disabled") {
		t.Error("unconfigured Telegram buttons should be disabled")
	}
}

func TestTelegramLinksRenderWhenConfigured(t *testing.T) {
	t.Setenv(envTelegramChannel, "https://t.me/guison_channel")
	t.Setenv(envTelegramDeveloper, "https://t.me/guison_dev")

	response := httptest.NewRecorder()
	newSiteServer().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	body := response.Body.String()

	for _, want := range []string{
		"https://t.me/guison_channel", "https://t.me/guison_dev",
		`rel="noopener noreferrer external"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("configured page missing %q", want)
		}
	}
}

// Only http(s) destinations are accepted, so a hostile value cannot inject a scheme.
func TestCommunityLinkRejectsUnsafeSchemes(t *testing.T) {
	for _, value := range []string{"javascript:alert(1)", "data:text/html,x", "tg://resolve", "  "} {
		t.Setenv(envTelegramChannel, value)
		if link := communityLink(envTelegramChannel, "X"); link.Configured {
			t.Errorf("value %q must not be accepted as a link", value)
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
