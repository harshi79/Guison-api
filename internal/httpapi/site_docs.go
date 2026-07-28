package httpapi

// docsHTML is the full developer documentation page.
const docsHTML = `
{{define "docs"}}{{template "head" .}}{{template "nav" .}}
<main id="main" class="wrap">
<div class="docs-layout">

  <aside class="toc" aria-label="Documentation sections">
    <h4>On this page</h4>
    <ul>
      <li><a href="#overview">Overview</a></li>
      <li><a href="#base-url">Base URL</a></li>
      <li><a href="#quick-start">Quick Start</a></li>
      <li><a href="#auth">Authentication</a></li>
      <li><a href="#input">Input rules</a></li>
      <li><a href="#endpoints">Endpoints</a></li>
      <li><a href="#examples">Request examples</a></li>
      <li><a href="#schema">Response schema</a></li>
      <li><a href="#fields">Field descriptions</a></li>
      <li><a href="#errors">Error responses</a></li>
      <li><a href="#health">Health endpoints</a></li>
      <li><a href="#freshness">Data freshness</a></li>
      <li><a href="#attribution">Source attribution</a></li>
      <li><a href="#faq">FAQ</a></li>
    </ul>
  </aside>

  <div class="doc-body">
    <header style="margin-bottom:42px">
      <span class="section-tag">Documentation</span>
      <h1 style="font-size:clamp(2rem,4.4vw,2.7rem)">Guison API</h1>
      <p class="lead" style="margin-bottom:0">Everything you need to integrate BIN/IIN lookup. No API key, no SDK, one JSON shape.</p>
    </header>

    <section class="doc-sec" id="overview">
      <h2>Overview</h2>
      <p>Guison API resolves a payment card BIN/IIN &mdash; the leading 6 to 8 digits of a card number &mdash; to metadata about the issuing range. A single <code>GET</code> request returns the card scheme, funding type, product level, issuing bank and country as JSON.</p>
      <p>Guison stores BIN data as <strong>ranges</strong> in PostgreSQL. When you query, it finds every range covering your digits and returns the most specific match, so an 8-digit query can resolve to a narrower record than the equivalent 6-digit prefix.</p>
      <p><strong>Guison does not verify cards.</strong> It performs metadata lookup only. It cannot tell you whether a card exists, is active, or has funds.</p>
    </section>

    <section class="doc-sec" id="base-url">
      <h2>Base URL</h2>
      <div class="code">
        <div class="code-head"><span class="code-title">Base URL</span><button class="mini" type="button" data-copy="#doc-base">Copy</button></div>
        <pre><code id="doc-base">{{.BaseURL}}</code></pre>
      </div>
      <p>All endpoints are relative to this base. Requests are served over HTTPS in production. Public routes send permissive CORS headers (<code>Access-Control-Allow-Origin: *</code>, methods <code>GET, OPTIONS</code>), so you can call Guison directly from a browser.</p>
    </section>

    <section class="doc-sec" id="quick-start">
      <h2>Quick Start</h2>
      <p>Send 6 to 8 digits as the path. That is the entire integration.</p>
      <div class="code">
        <div class="code-head"><span class="code-title">bash</span><button class="mini" type="button" data-copy="#doc-qs">Copy</button></div>
        <pre><code id="doc-qs"><span class="tok-c">curl</span> {{.BaseURL}}/45717360</code></pre>
      </div>
      <p>A match returns <code>200</code> with the JSON body described in <a href="#schema">Response schema</a>. No match returns <code>404</code>. Malformed input returns <code>400</code>.</p>
    </section>

    <section class="doc-sec" id="auth">
      <h2>Authentication</h2>
      <p><strong>None required.</strong> Public BIN lookup needs no API key, token or header. Send the request and read the response.</p>
      <p>The only authenticated surface is <code>/data</code>, a protected internal administration interface used to manage datasets. It is not part of the public API and is not documented here.</p>
    </section>

    <section class="doc-sec" id="input">
      <h2>BIN/IIN input rules</h2>
      <ul>
        <li>Must be <strong>6 to 8 digits</strong>.</li>
        <li>Digits <code>0-9</code> only. No spaces, dashes or letters.</li>
        <li>Leading zeros are preserved &mdash; always send the value as a string, not a number.</li>
        <li>Anything shorter than 6, longer than 8, or non-numeric returns <code>400 invalid_iin</code>.</li>
      </ul>
      <div class="note" style="margin-top:18px">
        <strong>Never send a full card number.</strong>
        <p style="margin:8px 0 0">The 8-digit ceiling is enforced by the API. Truncate to the first 6 to 8 digits on your side before calling Guison, and never log or transmit a full PAN.</p>
      </div>
    </section>

    <section class="doc-sec" id="endpoints">
      <h2>Endpoints</h2>
      <table class="api">
        <thead><tr><th>Method &amp; path</th><th>Returns</th><th>Description</th></tr></thead>
        <tbody>
          <tr><td>GET /{bin}</td><td>JSON</td><td>Primary lookup for a 6&ndash;8 digit BIN/IIN.</td></tr>
          <tr><td>GET /v1/bin/{bin}</td><td>JSON</td><td>Versioned lookup. Identical behaviour and response.</td></tr>
          <tr><td>GET /health</td><td>text/plain</td><td>Service-alive check for uptime monitors. Always <code>OK</code> while the process runs.</td></tr>
          <tr><td>GET /healthz</td><td>JSON</td><td>Application health with uptime in seconds.</td></tr>
          <tr><td>GET /readyz</td><td>JSON</td><td>Whether usable BIN data is currently available.</td></tr>
          <tr><td>GET /docs</td><td>HTML</td><td>This documentation page.</td></tr>
        </tbody>
      </table>
      <p>Both lookup routes are interchangeable. Use <code>/v1/bin/{bin}</code> if you prefer an explicitly versioned path.</p>
      <p>Lookup responses are sent with <code>Cache-Control: no-store</code> so a dataset update is visible on the very next request.</p>
    </section>

    <section class="doc-sec" id="examples">
      <h2>Request examples</h2>
      <div class="tabs" role="tablist" aria-label="Code language" data-tabs>
        <button class="tab" role="tab" id="tab-curl" data-panel="p-curl" aria-selected="true" aria-controls="p-curl">cURL</button>
        <button class="tab" role="tab" id="tab-js" data-panel="p-js" aria-selected="false" tabindex="-1" aria-controls="p-js">JavaScript</button>
        <button class="tab" role="tab" id="tab-py" data-panel="p-py" aria-selected="false" tabindex="-1" aria-controls="p-py">Python</button>
      </div>

      <div id="p-curl" class="panel" role="tabpanel" aria-labelledby="tab-curl">
        <div class="code">
          <div class="code-head"><span class="code-title">cURL</span><button class="mini" type="button" data-copy="#c-curl">Copy</button></div>
          <pre><code id="c-curl"><span class="tok-m"># Primary route</span>
<span class="tok-c">curl</span> {{.BaseURL}}/45717360

<span class="tok-m"># Versioned route</span>
<span class="tok-c">curl</span> {{.BaseURL}}/v1/bin/45717360

<span class="tok-m"># Show the HTTP status too</span>
<span class="tok-c">curl</span> -i {{.BaseURL}}/457173</code></pre>
        </div>
      </div>

      <div id="p-js" class="panel" role="tabpanel" aria-labelledby="tab-js" hidden>
        <div class="code">
          <div class="code-head"><span class="code-title">JavaScript &middot; fetch</span><button class="mini" type="button" data-copy="#c-js">Copy</button></div>
          <pre><code id="c-js"><span class="tok-k">async function</span> <span class="tok-c">lookupBin</span>(bin) {
  <span class="tok-k">const</span> response = <span class="tok-k">await</span> <span class="tok-c">fetch</span>(<span class="tok-s">` + "`" + `{{.BaseURL}}/${bin}` + "`" + `</span>);

  <span class="tok-k">if</span> (response.status === <span class="tok-n">404</span>) <span class="tok-k">return null</span>;

  <span class="tok-k">const</span> body = <span class="tok-k">await</span> response.<span class="tok-c">json</span>();

  <span class="tok-k">if</span> (!response.ok) {
    <span class="tok-k">throw new</span> <span class="tok-c">Error</span>(body.error?.message ?? <span class="tok-s">"Lookup failed"</span>);
  }
  <span class="tok-k">return</span> body;
}

<span class="tok-k">const</span> card = <span class="tok-k">await</span> <span class="tok-c">lookupBin</span>(<span class="tok-s">"45717360"</span>);
console.<span class="tok-c">log</span>(card.scheme, card.bank.name, card.country.name);</code></pre>
        </div>
      </div>

      <div id="p-py" class="panel" role="tabpanel" aria-labelledby="tab-py" hidden>
        <div class="code">
          <div class="code-head"><span class="code-title">Python &middot; requests</span><button class="mini" type="button" data-copy="#c-py">Copy</button></div>
          <pre><code id="c-py"><span class="tok-k">import</span> requests

BASE = <span class="tok-s">"{{.BaseURL}}"</span>

<span class="tok-k">def</span> <span class="tok-c">lookup_bin</span>(bin_value: <span class="tok-k">str</span>):
    response = requests.<span class="tok-c">get</span>(<span class="tok-s">f"{BASE}/{bin_value}"</span>, timeout=<span class="tok-n">10</span>)

    <span class="tok-k">if</span> response.status_code == <span class="tok-n">404</span>:
        <span class="tok-k">return None</span>

    response.<span class="tok-c">raise_for_status</span>()
    <span class="tok-k">return</span> response.<span class="tok-c">json</span>()


card = <span class="tok-c">lookup_bin</span>(<span class="tok-s">"45717360"</span>)
<span class="tok-k">if</span> card:
    <span class="tok-c">print</span>(card[<span class="tok-s">"scheme"</span>], card[<span class="tok-s">"bank"</span>][<span class="tok-s">"name"</span>])</code></pre>
        </div>
      </div>
    </section>

    <section class="doc-sec" id="schema">
      <h2>Response schema</h2>
      <p>A successful lookup returns <code>200</code> with this structure. Values below are illustrative; unknown strings are <code>""</code> and unknown optional values are <code>null</code>.</p>
      <div class="code">
        <div class="code-head"><span class="code-title">200 OK &middot; application/json</span><button class="mini" type="button" data-copy="#c-schema">Copy</button></div>
        <pre><code id="c-schema">{
  <span class="tok-k">"iin"</span>: <span class="tok-s">"45717360"</span>,
  <span class="tok-k">"match"</span>: {
    <span class="tok-k">"start"</span>: <span class="tok-s">"457173"</span>,
    <span class="tok-k">"end"</span>: <span class="tok-s">"457173"</span>,
    <span class="tok-k">"length"</span>: <span class="tok-n">6</span>
  },
  <span class="tok-k">"number"</span>: {
    <span class="tok-k">"length"</span>: <span class="tok-n">16</span>,
    <span class="tok-k">"luhn"</span>: <span class="tok-n">true</span>
  },
  <span class="tok-k">"scheme"</span>: <span class="tok-s">"visa"</span>,
  <span class="tok-k">"brand"</span>: <span class="tok-s">"Visa/Dankort"</span>,
  <span class="tok-k">"type"</span>: <span class="tok-s">"debit"</span>,
  <span class="tok-k">"level"</span>: <span class="tok-s">""</span>,
  <span class="tok-k">"prepaid"</span>: <span class="tok-n">null</span>,
  <span class="tok-k">"country"</span>: {
    <span class="tok-k">"alpha2"</span>: <span class="tok-s">"DK"</span>,
    <span class="tok-k">"alpha3"</span>: <span class="tok-s">"DNK"</span>,
    <span class="tok-k">"name"</span>: <span class="tok-s">"Denmark"</span>,
    <span class="tok-k">"emoji"</span>: <span class="tok-s">"&#127465;&#127472;"</span>,
    <span class="tok-k">"currency"</span>: <span class="tok-s">"DKK"</span>,
    <span class="tok-k">"latitude"</span>: <span class="tok-n">56</span>,
    <span class="tok-k">"longitude"</span>: <span class="tok-n">10</span>
  },
  <span class="tok-k">"bank"</span>: {
    <span class="tok-k">"name"</span>: <span class="tok-s">"Jyske Bank"</span>,
    <span class="tok-k">"url"</span>: <span class="tok-s">"www.jyskebank.dk"</span>,
    <span class="tok-k">"phone"</span>: <span class="tok-s">"+45 89 89 89 89"</span>,
    <span class="tok-k">"city"</span>: <span class="tok-s">"Silkeborg"</span>,
    <span class="tok-k">"logo"</span>: <span class="tok-s">""</span>
  },
  <span class="tok-k">"source"</span>: {
    <span class="tok-k">"id"</span>: <span class="tok-s">"bin-list-data"</span>,
    <span class="tok-k">"repository"</span>: <span class="tok-s">"{{.SourceRepository}}"</span>,
    <span class="tok-k">"commit"</span>: <span class="tok-s">"023a4f6"</span>,
    <span class="tok-k">"synced_at"</span>: <span class="tok-s">"2025-02-11T13:40:06Z"</span>
  }
}</code></pre>
      </div>
    </section>

    <section class="doc-sec" id="fields">
      <h2>Field descriptions</h2>
      <table class="api">
        <thead><tr><th>Field</th><th>Type</th><th>Description</th></tr></thead>
        <tbody>
          <tr><td>iin</td><td>string</td><td>The BIN/IIN exactly as you sent it.</td></tr>
          <tr><td>match.start</td><td>string</td><td>First BIN in the matched range.</td></tr>
          <tr><td>match.end</td><td>string</td><td>Last BIN in the matched range. Equals <code>start</code> for single-BIN records.</td></tr>
          <tr><td>match.length</td><td>number</td><td>Digit width of the matched range (6, 7 or 8).</td></tr>
          <tr><td>number.length</td><td>number | null</td><td>Expected full card number length. <code>null</code> when unknown.</td></tr>
          <tr><td>number.luhn</td><td>boolean | null</td><td>Whether the Luhn check digit applies. <code>null</code> when unknown.</td></tr>
          <tr><td>scheme</td><td>string</td><td>Card scheme or network, e.g. <code>visa</code>, <code>mastercard</code>. Empty when unknown.</td></tr>
          <tr><td>brand</td><td>string</td><td>Product brand, e.g. <code>Visa/Dankort</code>. Empty when unknown.</td></tr>
          <tr><td>type</td><td>string</td><td>Funding type, typically <code>debit</code> or <code>credit</code>. Empty when unknown.</td></tr>
          <tr><td>level</td><td>string</td><td>Product level, e.g. <code>classic</code>, <code>platinum</code>. Empty when unknown.</td></tr>
          <tr><td>prepaid</td><td>boolean | null</td><td>Whether the range is prepaid. <code>null</code> when the dataset does not say.</td></tr>
          <tr><td>country.alpha2</td><td>string</td><td>ISO 3166-1 alpha-2 country code.</td></tr>
          <tr><td>country.alpha3</td><td>string</td><td>ISO 3166-1 alpha-3 country code.</td></tr>
          <tr><td>country.name</td><td>string</td><td>Country name.</td></tr>
          <tr><td>country.emoji</td><td>string</td><td>Flag emoji derived from the alpha-2 code.</td></tr>
          <tr><td>country.currency</td><td>string</td><td>Country currency code when available.</td></tr>
          <tr><td>country.latitude</td><td>number | null</td><td>Approximate country latitude.</td></tr>
          <tr><td>country.longitude</td><td>number | null</td><td>Approximate country longitude.</td></tr>
          <tr><td>bank.name</td><td>string</td><td>Issuing bank name. Empty when unknown.</td></tr>
          <tr><td>bank.url</td><td>string</td><td>Issuer website.</td></tr>
          <tr><td>bank.phone</td><td>string</td><td>Issuer contact phone.</td></tr>
          <tr><td>bank.city</td><td>string</td><td>Issuer city.</td></tr>
          <tr><td>bank.logo</td><td>string</td><td>Issuer logo reference when available.</td></tr>
          <tr><td>source.id</td><td>string</td><td>Configured source identifier the record came from.</td></tr>
          <tr><td>source.repository</td><td>string</td><td>Upstream repository for that source.</td></tr>
          <tr><td>source.commit</td><td>string</td><td>Commit of the imported dataset version.</td></tr>
          <tr><td>source.synced_at</td><td>string</td><td>RFC 3339 timestamp of the last successful import.</td></tr>
        </tbody>
      </table>
      <h3>Nullable and empty values</h3>
      <p>Guison never invents data. Two conventions apply:</p>
      <ul>
        <li><strong>String fields</strong> return <code>""</code> when the dataset has no value.</li>
        <li><strong>Optional typed fields</strong> &mdash; <code>prepaid</code>, <code>number.length</code>, <code>number.luhn</code>, <code>country.latitude</code>, <code>country.longitude</code> &mdash; return <code>null</code>.</li>
      </ul>
      <p>Always guard against both. In JavaScript prefer <code>card.bank?.name || "Unknown"</code>.</p>
    </section>

    <section class="doc-sec" id="errors">
      <h2>Error responses</h2>
      <p>Errors use a consistent envelope with a stable machine-readable <code>code</code>:</p>
      <div class="code">
        <div class="code-head"><span class="code-title">Error envelope</span><button class="mini" type="button" data-copy="#c-err">Copy</button></div>
        <pre><code id="c-err">{
  <span class="tok-k">"error"</span>: {
    <span class="tok-k">"code"</span>: <span class="tok-s">"invalid_iin"</span>,
    <span class="tok-k">"message"</span>: <span class="tok-s">"IIN must contain 6 to 8 digits"</span>
  }
}</code></pre>
      </div>
      <table class="api">
        <thead><tr><th>Status</th><th>Code</th><th>When it happens</th></tr></thead>
        <tbody>
          <tr><td>400</td><td>invalid_iin</td><td>Input is not 6&ndash;8 characters, or contains a non-digit. Message is <code>IIN must contain 6 to 8 digits</code> or <code>IIN must contain only digits</code>.</td></tr>
          <tr><td>404</td><td>not_found</td><td>Input is valid but no record covers it. Message is <code>No record covers this BIN/IIN</code>.</td></tr>
          <tr><td>500</td><td>internal_error</td><td>Unexpected server or database failure. Message is <code>An internal error occurred</code>; details are logged server-side, never returned.</td></tr>
          <tr><td>503</td><td>not_ready</td><td>Returned by <code>/readyz</code> when no usable dataset is loaded yet.</td></tr>
        </tbody>
      </table>
      <p>Treat <code>404</code> as a normal outcome, not a failure &mdash; it simply means the BIN is not in the dataset.</p>
    </section>

    <section class="doc-sec" id="health">
      <h2>Health endpoints</h2>
      <h3>GET /health</h3>
      <p>Minimal liveness probe for uptime monitors. Returns <code>200</code> and the plain text body <code>OK</code>. It does not query the database, so it stays fast and cheap.</p>
      <div class="code"><div class="code-head"><span class="code-title">200 OK &middot; text/plain</span></div><pre><code>OK</code></pre></div>

      <h3>GET /healthz</h3>
      <p>Application health with process uptime.</p>
      <div class="code"><div class="code-head"><span class="code-title">200 OK &middot; application/json</span></div><pre><code>{ <span class="tok-k">"status"</span>: <span class="tok-s">"ok"</span>, <span class="tok-k">"uptime_seconds"</span>: <span class="tok-n">86400</span> }</code></pre></div>

      <h3>GET /readyz</h3>
      <p>Reports whether lookups can actually be served. Use this, not <code>/health</code>, to decide if Guison is usable.</p>
      <div class="code"><div class="code-head"><span class="code-title">200 OK &middot; ready</span></div><pre><code>{ <span class="tok-k">"status"</span>: <span class="tok-s">"ready"</span> }</code></pre></div>
      <div class="code"><div class="code-head"><span class="code-title">503 Service Unavailable &middot; not ready</span></div><pre><code>{ <span class="tok-k">"error"</span>: { <span class="tok-k">"code"</span>: <span class="tok-s">"not_ready"</span>, <span class="tok-k">"message"</span>: <span class="tok-s">"..."</span> } }</code></pre></div>
    </section>

    <section class="doc-sec" id="freshness">
      <h2>Data freshness</h2>
      <p>Guison checks each configured source on a schedule. For every source it reads the latest upstream commit for the configured file and compares it with the commit already stored. If nothing changed, nothing is downloaded.</p>
      <p>When a source has changed, Guison downloads that exact version, parses and validates it into a temporary table, and replaces that source's records in a single transaction. If download, parsing or validation fails, the previously imported records keep serving and the error is recorded.</p>
      {{if .LastSynced}}<p>The most recent successful import completed on <strong>{{.LastSynced}}</strong>.</p>{{end}}
      <p>Because Guison mirrors upstream open datasets, freshness is bounded by how often those datasets publish. Newly issued BIN ranges can take time to appear, and some may never be published publicly.</p>
    </section>

    <section class="doc-sec" id="attribution">
      <h2>Source attribution</h2>
      <p>Every successful lookup includes a <code>source</code> object identifying exactly where the record came from &mdash; the source id, upstream repository, imported commit and sync timestamp. This makes results auditable and reproducible.</p>
      <p>The current primary dataset is <a href="https://github.com/{{.SourceRepository}}" target="_blank" rel="noopener noreferrer external">{{.SourceRepository}}</a>, published under the
        <a href="https://creativecommons.org/licenses/by/4.0/" target="_blank" rel="noopener noreferrer external">Creative Commons Attribution 4.0 International licence</a>.</p>
      <p>If you redistribute data obtained from Guison, CC BY 4.0 requires that you credit the upstream dataset and indicate that changes were made. Guison normalises the source CSV into range records and serves it as JSON.</p>
      <div class="note" style="margin-top:18px">
        <strong>Not authoritative.</strong>
        <p style="margin:8px 0 0">BIN/IIN data is community-maintained open data. It may be incomplete, stale or inaccurate, and it is not real-time payment network data. Do not use Guison as authoritative payment verification or as a sole fraud or compliance control.</p>
      </div>
    </section>

    <section class="doc-sec" id="faq">
      <h2>FAQ</h2>
      <div class="faq-list">
        <details class="faq">
          <summary>Do I need an API key?{{template "caret" .}}</summary>
          <div class="faq-body"><p>No. Public BIN lookup requires no key or authentication. Only the internal <code>/data</code> administration interface is protected.</p></div>
        </details>
        <details class="faq">
          <summary>Which BIN lengths are supported?{{template "caret" .}}</summary>
          <div class="faq-body"><p>6, 7 and 8 digits. Longer or shorter input returns <code>400 invalid_iin</code>, which also means a full card number cannot be submitted.</p></div>
        </details>
        <details class="faq">
          <summary>Does an 8-digit query give a better answer than 6?{{template "caret" .}}</summary>
          <div class="faq-body"><p>It can. Guison returns the most specific range covering your input, so if an 8-digit range exists you get that record. If only a 6-digit range exists, both queries return the same thing. Check <code>match.length</code> to see which resolved.</p></div>
        </details>
        <details class="faq">
          <summary>Can Guison tell me if a card is real or valid?{{template "caret" .}}</summary>
          <div class="faq-body"><p>No. Guison performs BIN/IIN metadata lookup only. It does not verify that a payment card or account exists, is active or has funds, and it cannot authorise a transaction.</p></div>
        </details>
        <details class="faq">
          <summary>Why is a field null or empty?{{template "caret" .}}</summary>
          <div class="faq-body"><p>The dataset has no value for it. Guison does not guess. Strings become <code>""</code> and optional typed fields become <code>null</code>.</p></div>
        </details>
        <details class="faq">
          <summary>Is there a rate limit?{{template "caret" .}}</summary>
          <div class="faq-body"><p>No hard published limit today. Please cache responses where you can &mdash; BIN data changes slowly &mdash; and avoid unnecessary bursts so the service stays fast for everyone.</p></div>
        </details>
        <details class="faq">
          <summary>Can I call Guison from the browser?{{template "caret" .}}</summary>
          <div class="faq-body"><p>Yes. Public routes send <code>Access-Control-Allow-Origin: *</code> with methods <code>GET, OPTIONS</code>, so client-side <code>fetch</code> works without a proxy.</p></div>
        </details>
        <details class="faq">
          <summary>How should I handle errors?{{template "caret" .}}</summary>
          <div class="faq-body"><p>Treat <code>404</code> as "not in dataset" rather than an error. Retry <code>500</code> and <code>503</code> with backoff. Fix <code>400</code> by validating that you are sending 6 to 8 digits before the call.</p></div>
        </details>
      </div>
    </section>

    <p style="color:var(--faint);font-size:.9rem;border-top:1px solid var(--line);padding-top:22px">
      Something unclear or missing? <a href="/#community">Reach out on Telegram</a> and it will be added.
    </p>
  </div>
</div>
</main>
{{template "footer" .}}
{{end}}
`
