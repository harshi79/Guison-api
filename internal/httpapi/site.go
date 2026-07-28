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
//	TELEGRAM_PRIVATE_URL   — private community invite link
//	DEVELOPER_TELEGRAM_URL — direct contact for the maintainer
//
// When a value is missing the site renders a clearly marked placeholder and
// disables the link, so a fake username is never shipped to production.
// Destinations are rendered as labelled buttons; the raw URL is never printed
// as visible page copy.
const (
	envTelegramChannel   = "TELEGRAM_CHANNEL_URL"
	envTelegramPrivate   = "TELEGRAM_PRIVATE_URL"
	envTelegramDeveloper = "DEVELOPER_TELEGRAM_URL"
)

// CommunityLink is one external community destination. Label is what the user
// sees; URL is only ever used as a link target, never printed as page copy.
type CommunityLink struct {
	Label       string
	URL         string
	Configured  bool
	Placeholder string
	Style       string
}

// communityLink reads one destination from the environment. Only absolute
// http(s) URLs are accepted, so a hostile or malformed value (javascript:,
// data:, tg://) can never become a link target.
func communityLink(envName, label, style string) CommunityLink {
	link := CommunityLink{Label: label, Placeholder: envName, Style: style}
	value := strings.TrimSpace(os.Getenv(envName))
	if value == "" || !(strings.HasPrefix(value, "https://") || strings.HasPrefix(value, "http://")) {
		return link
	}
	link.URL = value
	link.Configured = true
	return link
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
	TelegramPrivate  CommunityLink
	TelegramDev      CommunityLink
	// CommunityLinks is the ordered list rendered as buttons.
	CommunityLinks []CommunityLink
	// UnsetCommunityVars names the variables still needing configuration.
	UnsetCommunityVars []string
	Year               int
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
		TelegramChannel:  communityLink(envTelegramChannel, "Official Channel", "btn-primary"),
		TelegramPrivate:  communityLink(envTelegramPrivate, "Private Community", "btn-ghost"),
		TelegramDev:      communityLink(envTelegramDeveloper, "Contact Developer", "btn-ghost"),
		Year:             time.Now().Year(),
	}
	view.CommunityLinks = []CommunityLink{view.TelegramChannel, view.TelegramPrivate, view.TelegramDev}
	for _, link := range view.CommunityLinks {
		if !link.Configured {
			view.UnsetCommunityVars = append(view.UnsetCommunityVars, link.Placeholder)
		}
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
