package httpapi

import (
	"context"
	"embed"
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

//go:embed assets
var siteAssets embed.FS

// Community links are configuration, not code. Set these environment variables
// in the hosting provider to publish real destinations:
//
//	TELEGRAM_CHANNEL_URL   — public Telegram channel for announcements
//	DEVELOPER_TELEGRAM_URL — direct contact for the maintainer
//
// When a value is missing the site renders a clearly marked placeholder and
// disables the link, so a fake username is never shipped to production.
const (
	envTelegramChannel   = "TELEGRAM_CHANNEL_URL"
	envTelegramDeveloper = "DEVELOPER_TELEGRAM_URL"
)

// CommunityLink is one external community destination.
type CommunityLink struct {
	URL         string
	Configured  bool
	Placeholder string
}

func communityLink(envName, placeholder string) CommunityLink {
	value := strings.TrimSpace(os.Getenv(envName))
	if value == "" || !(strings.HasPrefix(value, "https://") || strings.HasPrefix(value, "http://")) {
		return CommunityLink{Configured: false, Placeholder: placeholder}
	}
	return CommunityLink{URL: value, Configured: true, Placeholder: placeholder}
}

// siteView is the data passed to the public page templates.
type siteView struct {
	Page             string
	Title            string
	Description      string
	BaseURL          string
	RecordCount      string
	HasRecordCount   bool
	SourceRepository string
	LastSynced       string
	TelegramChannel  CommunityLink
	TelegramDev      CommunityLink
	Year             int
}

var sitePages = template.Must(template.New("site").Funcs(template.FuncMap{
	"safeURL": func(value string) template.URL { return template.URL(value) },
}).Parse(layoutHTML + homeHTML + docsHTML))

// publicBaseURL derives the canonical base URL from the incoming request so
// examples are copy-pasteable in local development and in production.
func publicBaseURL(r *http.Request) string {
	scheme := "https"
	if forwarded := r.Header.Get("X-Forwarded-Proto"); forwarded != "" {
		scheme = strings.TrimSpace(strings.Split(forwarded, ",")[0])
	} else if r.TLS == nil && isLocalHost(r.Host) {
		scheme = "http"
	}
	host := r.Host
	if forwarded := r.Header.Get("X-Forwarded-Host"); forwarded != "" {
		host = strings.TrimSpace(strings.Split(forwarded, ",")[0])
	}
	if host == "" {
		host = "guison-api.onrender.com"
	}
	return scheme + "://" + host
}

func isLocalHost(host string) bool {
	return strings.HasPrefix(host, "localhost") || strings.HasPrefix(host, "127.0.0.1") || strings.HasPrefix(host, "[::1]")
}

// humaniseCount renders a record total as a compact marketing-safe figure.
func humaniseCount(records int64) string {
	switch {
	case records >= 1_000_000:
		return strconv.FormatFloat(float64(records)/1_000_000, 'f', 1, 64) + "M+"
	case records >= 1_000:
		return strconv.FormatInt(records/1_000, 10) + "K+"
	default:
		return strconv.FormatInt(records, 10)
	}
}

// buildSiteView collects live figures without failing the page when the
// database is unavailable: the site degrades to general wording instead.
func (s *Server) buildSiteView(r *http.Request, page string) siteView {
	title := "Guison API — Fast BIN/IIN lookup"
	description := "A fast, simple BIN/IIN lookup API. Send 6 to 8 digits and get the card scheme, type, issuing bank and country as JSON. No API key required."
	if page == "docs" {
		title = "Documentation — Guison API"
		description = "Developer documentation for Guison API: endpoints, request examples, response schema, field descriptions and error handling."
	}

	view := siteView{
		Page:             page,
		Title:            title,
		Description:      description,
		BaseURL:          publicBaseURL(r),
		SourceRepository: "venelinkochev/bin-list-data",
		TelegramChannel:  communityLink(envTelegramChannel, "TELEGRAM_CHANNEL_URL"),
		TelegramDev:      communityLink(envTelegramDeveloper, "DEVELOPER_TELEGRAM_URL"),
		Year:             time.Now().Year(),
	}

	ctx, cancel := context.WithTimeout(r.Context(), 1500*time.Millisecond)
	defer cancel()
	stats, err := s.store.Stats(ctx)
	if err != nil {
		slog.Warn("site stats unavailable", "error", err)
		return view
	}
	if stats.Records > 0 {
		view.RecordCount = humaniseCount(stats.Records)
		view.HasRecordCount = true
	}
	if stats.LastSyncedAt != nil {
		view.LastSynced = stats.LastSyncedAt.UTC().Format("2 January 2006")
	}
	for _, source := range stats.Sources {
		if source.Repository != "" && source.Priority < 1000 {
			view.SourceRepository = source.Repository
			break
		}
	}
	return view
}

func (s *Server) renderSite(w http.ResponseWriter, r *http.Request, templateName, page string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; img-src 'self' data:; style-src 'self'; script-src 'self'; "+
			"connect-src 'self'; form-action 'none'; base-uri 'none'; frame-ancestors 'none'")

	if err := sitePages.ExecuteTemplate(w, templateName, s.buildSiteView(r, page)); err != nil {
		slog.Error("render site page", "template", templateName, "error", err)
	}
}

func (s *Server) homePage(w http.ResponseWriter, r *http.Request) {
	s.renderSite(w, r, "home", "home")
}

func (s *Server) docsPage(w http.ResponseWriter, r *http.Request) {
	s.renderSite(w, r, "docs", "docs")
}

// staticAssets serves the embedded CSS, JS and artwork with long cache lifetimes.
func staticAssets() http.Handler {
	fileServer := http.FileServer(http.FS(siteAssets))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		fileServer.ServeHTTP(w, r)
	})
}
