package httpapi

// layoutHTML holds the shared chrome: head, navigation and footer.
const layoutHTML = `
{{define "head"}}<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<meta name="description" content="{{.Description}}">
<meta name="color-scheme" content="dark">
<meta name="theme-color" content="#0a0c0a">
<meta property="og:title" content="{{.Title}}">
<meta property="og:description" content="{{.Description}}">
<meta property="og:type" content="website">
<link rel="stylesheet" href="/assets/site.css">
<link rel="icon" href="data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 100 100'><rect width='100' height='100' rx='22' fill='%23b4ff2e'/><text y='72' x='50' text-anchor='middle' font-size='62' font-family='monospace' font-weight='bold' fill='%230a0c0a'>G</text></svg>">
</head>
<body>
<a class="skip" href="#main">Skip to content</a>
{{end}}

{{define "nav"}}
<header class="nav">
  <div class="wrap nav-in">
    <a class="brand" href="/"><span class="brand-mark" aria-hidden="true">G</span> Guison<span style="color:var(--lime)">API</span></a>
    <button class="burger" type="button" aria-label="Toggle navigation" aria-expanded="false" aria-controls="nav-links">
      <span></span><span></span><span></span>
    </button>
    <nav id="nav-links" class="nav-links" aria-label="Main">
      <a href="/{{if ne .Page "home"}}{{end}}"{{if eq .Page "home"}} aria-current="page"{{end}}>Home</a>
      <a href="/#try">Try API</a>
      <a href="/#features">Features</a>
      <a href="/docs"{{if eq .Page "docs"}} aria-current="page"{{end}}>Docs</a>
      <a href="/#faq">FAQ</a>
      <a href="/#community">Telegram</a>
      <a class="nav-cta" href="/docs">Read Docs</a>
    </nav>
  </div>
</header>
{{end}}

{{define "footer"}}
<footer class="footer">
  <div class="wrap">
    <div class="foot-grid">
      <div class="foot-brand">
        <a class="brand" href="/"><span class="brand-mark" aria-hidden="true">G</span> Guison<span style="color:var(--lime)">API</span></a>
        <p>A fast, simple BIN/IIN lookup API built on openly licensed payment card data.</p>
      </div>
      <div class="foot">
        <h4>Product</h4>
        <ul>
          <li><a href="/docs">Documentation</a></li>
          <li><a href="/#try">Try the API</a></li>
          <li><a href="/#endpoints">Endpoints</a></li>
          <li><a href="/#faq">FAQ</a></li>
        </ul>
      </div>
      <div class="foot">
        <h4>Service</h4>
        <ul>
          <li><a href="/health">Health</a></li>
          <li><a href="/healthz">Health details</a></li>
          <li><a href="/readyz">Readiness</a></li>
          <li><a href="/45717360">Example lookup</a></li>
        </ul>
      </div>
      <div class="foot">
        <h4>Community</h4>
        <ul>
          <li>{{if .TelegramChannel.Configured}}<a href="{{.TelegramChannel.URL | safeURL}}" target="_blank" rel="noopener noreferrer external">Telegram Channel</a>{{else}}<span style="color:var(--faint);font-size:.89rem">Telegram Channel (unset)</span>{{end}}</li>
          <li>{{if .TelegramDev.Configured}}<a href="{{.TelegramDev.URL | safeURL}}" target="_blank" rel="noopener noreferrer external">Developer Telegram</a>{{else}}<span style="color:var(--faint);font-size:.89rem">Developer Telegram (unset)</span>{{end}}</li>
          <li><a href="https://github.com/{{.SourceRepository}}" target="_blank" rel="noopener noreferrer external">Data Source</a></li>
        </ul>
      </div>
    </div>
    <div class="foot-bottom">
      <p>Built with open data. &copy; {{.Year}} Guison API.</p>
      <p>BIN data from <a href="https://github.com/{{.SourceRepository}}" target="_blank" rel="noopener noreferrer external">{{.SourceRepository}}</a>, licensed
         <a href="https://creativecommons.org/licenses/by/4.0/" target="_blank" rel="noopener noreferrer external">CC BY 4.0</a>. Modified: normalised into ranges and served as JSON.</p>
    </div>
  </div>
</footer>
<script src="/assets/site.js" defer></script>
</body>
</html>
{{end}}

{{define "tgicon"}}<svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M21.9 4.3 18.7 19.4c-.2 1.1-.9 1.3-1.8.8l-4.9-3.6-2.4 2.3c-.3.3-.5.5-1 .5l.4-5 9.1-8.2c.4-.4-.1-.6-.6-.2L6.3 13.1l-4.8-1.5c-1-.3-1.1-1 .2-1.5l18.8-7.2c.9-.3 1.6.2 1.4 1.4z"/></svg>{{end}}

{{define "caret"}}<svg class="caret" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><polyline points="6 9 12 15 18 9"/></svg>{{end}}
`
