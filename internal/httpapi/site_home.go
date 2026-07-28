package httpapi

// homeHTML is the public landing page.
const homeHTML = `
{{define "home"}}{{template "head" .}}{{template "nav" .}}
<main id="main">

<section class="hero">
  <div class="wrap hero-grid">
    <div>
      <div id="status" class="status">
        <span class="dot" aria-hidden="true"></span>
        <span class="status-text">Checking status&hellip;</span>
        <button type="button" class="status-refresh">refresh</button>
      </div>
      <h1>Look up any card <span class="accent">BIN or IIN</span> in one request.</h1>
      <p class="sub">Guison API is a fast, simple BIN/IIN lookup service. Send 6 to 8 digits, get back the scheme, card type, issuing bank and country as clean JSON. No API key, no SDK, no sign-up.</p>
      <div class="hero-actions">
        <a class="btn btn-primary" href="#try">Try API</a>
        <a class="btn btn-ghost" href="/docs">Read Docs</a>
      </div>
      <div class="hero-stats">
        <div class="stat">
          <b>{{if .HasRecordCount}}{{.RecordCount}}{{else}}Bulk{{end}}</b>
          <span>{{if .HasRecordCount}}BIN records{{else}}BIN dataset{{end}}</span>
        </div>
        <div class="stat"><b>6&ndash;8</b><span>Digit lookup</span></div>
        <div class="stat"><b>Auto</b><span>Data sync</span></div>
      </div>
    </div>
    <div class="hero-art">
      <img src="/assets/art/hero.webp" width="1600" height="913" alt="" role="presentation" fetchpriority="high" decoding="async">
    </div>
  </div>
</section>

<section class="section" id="try" style="padding-top:26px">
  <div class="wrap">
    <div class="reveal">
      <span class="section-tag">Live demo</span>
      <h2>Try Guison</h2>
      <p class="lead">Runs against this deployment in real time. Enter the first 6 to 8 digits of a card only &mdash; never a full card number.</p>
    </div>

    <div class="tester reveal">
      <form id="tester" novalidate>
        <div class="tester-form">
          <div class="field">
            <label class="lbl" for="bin-input">Enter 6&ndash;8 digit BIN/IIN</label>
            <input id="bin-input" name="bin" type="text" inputmode="numeric" autocomplete="off"
                   spellcheck="false" maxlength="8" placeholder="45717360"
                   pattern="[0-9]{6,8}" aria-describedby="bin-hint">
          </div>
          <div class="tester-actions">
            <button id="lookup-btn" class="btn btn-primary" type="submit"><span class="btn-label">Lookup</span></button>
          </div>
        </div>
        <p class="hint" id="bin-hint">Digits only. Guison accepts 6 to 8 digits and rejects anything longer, so a full card number can never be submitted.</p>
        <div class="chips">
          <button class="chip" type="button" data-example="45717360">45717360</button>
          <button class="chip" type="button" data-example="457173">457173</button>
          <button class="chip" type="button" data-example="411111">411111</button>
          <button class="chip" type="button" data-example="601100">601100</button>
        </div>
      </form>
      <div id="result" class="result" role="status" aria-live="polite" hidden></div>
    </div>
  </div>
</section>

<section class="section" id="features" style="padding-top:26px">
  <div class="wrap">
    <div class="reveal">
      <span class="section-tag">Capabilities</span>
      <h2>What Guison gives you</h2>
      <p class="lead">Every field below comes straight from the imported dataset. Guison never guesses a value &mdash; unknown fields return empty or <code>null</code>.</p>
    </div>
    <div class="cards reveal">
      <div class="card">
        <div class="ico" aria-hidden="true"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><ellipse cx="12" cy="5" rx="9" ry="3"/><path d="M3 5v14c0 1.7 4 3 9 3s9-1.3 9-3V5"/><path d="M3 12c0 1.7 4 3 9 3s9-1.3 9-3"/></svg></div>
        <h3>{{if .HasRecordCount}}{{.RecordCount}} records{{else}}Bulk BIN dataset{{end}}</h3>
        <p>A large imported dataset of BIN/IIN ranges stored in PostgreSQL and served directly from the database.</p>
      </div>
      <div class="card">
        <div class="ico" aria-hidden="true"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><circle cx="11" cy="11" r="7"/><path d="m20 20-3.5-3.5"/></svg></div>
        <h3>6&ndash;8 digit lookup</h3>
        <p>Query with 6, 7 or 8 digits. Guison resolves the most specific range that covers your input.</p>
      </div>
      <div class="card">
        <div class="ico" aria-hidden="true"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><rect x="2" y="5" width="20" height="14" rx="2"/><path d="M2 10h20"/></svg></div>
        <h3>Scheme &amp; network</h3>
        <p>Card scheme and product brand where the dataset provides them, such as Visa, Mastercard or Maestro.</p>
      </div>
      <div class="card">
        <div class="ico" aria-hidden="true"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M3 21h18"/><path d="M5 21V8l7-5 7 5v13"/><path d="M10 21v-6h4v6"/></svg></div>
        <h3>Issuing bank</h3>
        <p>Bank name plus website, phone and city when the source dataset includes them.</p>
      </div>
      <div class="card">
        <div class="ico" aria-hidden="true"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3a15 15 0 0 1 0 18M12 3a15 15 0 0 0 0 18"/></svg></div>
        <h3>Country information</h3>
        <p>ISO alpha-2 and alpha-3 codes, country name, flag emoji, currency and approximate coordinates.</p>
      </div>
      <div class="card">
        <div class="ico" aria-hidden="true"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M12 2v20M17 5H9.5a3.5 3.5 0 0 0 0 7h5a3.5 3.5 0 0 1 0 7H6"/></svg></div>
        <h3>Debit, credit &amp; level</h3>
        <p>Card funding type, product level such as classic or platinum, and a prepaid flag when known.</p>
      </div>
      <div class="card">
        <div class="ico" aria-hidden="true"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M21 12a9 9 0 1 1-3-6.7"/><polyline points="21 3 21 9 15 9"/></svg></div>
        <h3>Automatic data sync</h3>
        <p>Guison periodically checks its configured GitHub sources and imports validated updates automatically.</p>
      </div>
      <div class="card">
        <div class="ico" aria-hidden="true"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/></svg></div>
        <h3>Validated imports</h3>
        <p>Each update is parsed and validated in a staging table before it replaces live data, so a bad import never takes lookups down.</p>
      </div>
      <div class="card">
        <div class="ico" aria-hidden="true"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="m8 6-6 6 6 6M16 6l6 6-6 6"/></svg></div>
        <h3>Plain JSON responses</h3>
        <p>One predictable JSON shape, permissive CORS for browser calls, and no authentication to get started.</p>
      </div>
    </div>
  </div>
</section>

<div class="divider-art" aria-hidden="true">
  <img src="/assets/art/divider.webp" width="1600" height="857" alt="" loading="lazy" decoding="async">
</div>

<section class="section" id="endpoints" style="padding-top:26px">
  <div class="wrap">
    <div class="reveal">
      <span class="section-tag">Reference</span>
      <h2>Endpoints</h2>
      <p class="lead">Base URL <code>{{.BaseURL}}</code></p>
    </div>
    <div class="eps reveal">
      <div class="ep">
        <span class="verb">GET</span><span class="ep-path">/{bin}</span>
        <span class="ep-desc">Primary lookup. Accepts 6 to 8 digits, for example <code>/45717360</code>.</span>
      </div>
      <div class="ep">
        <span class="verb">GET</span><span class="ep-path">/v1/bin/{bin}</span>
        <span class="ep-desc">Versioned lookup. Identical response to the primary route.</span>
      </div>
      <div class="ep">
        <span class="verb">GET</span><span class="ep-path">/health</span>
        <span class="ep-desc">Simple service-alive endpoint used for uptime monitoring. Returns plain text.</span>
      </div>
      <div class="ep">
        <span class="verb">GET</span><span class="ep-path">/healthz</span>
        <span class="ep-desc">Application health and uptime information as JSON.</span>
      </div>
      <div class="ep">
        <span class="verb">GET</span><span class="ep-path">/readyz</span>
        <span class="ep-desc">Whether usable BIN data is currently available for lookups.</span>
      </div>
      <div class="ep">
        <span class="verb">GET</span><span class="ep-path">/docs</span>
        <span class="ep-desc">Full developer documentation for integrating Guison.</span>
      </div>
    </div>
    <p class="hint" style="margin-top:16px">Guison also exposes <code>/data</code>, a protected internal administration interface. It is not part of the public API.</p>
  </div>
</section>

<section class="section" id="example" style="padding-top:26px">
  <div class="wrap">
    <div class="reveal">
      <span class="section-tag">Quick start</span>
      <h2>One request, one response</h2>
      <p class="lead">No key, no headers, no client library. Full examples for cURL, JavaScript and Python live in the <a href="/docs">documentation</a>.</p>
    </div>
    <div class="cards reveal" style="grid-template-columns:repeat(auto-fit,minmax(320px,1fr))">
      <div class="code">
        <div class="code-head">
          <span class="code-title">Request</span>
          <button class="mini" type="button" data-copy="#ex-req">Copy</button>
        </div>
        <pre><code id="ex-req"><span class="tok-c">curl</span> {{.BaseURL}}/45717360</code></pre>
      </div>
      <div class="code">
        <div class="code-head">
          <span class="code-title">Response &middot; 200 OK</span>
          <button class="mini" type="button" data-copy="#ex-res">Copy</button>
        </div>
        <pre><code id="ex-res">{
  <span class="tok-k">"iin"</span>: <span class="tok-s">"45717360"</span>,
  <span class="tok-k">"match"</span>: { <span class="tok-k">"start"</span>: <span class="tok-s">"457173"</span>, <span class="tok-k">"end"</span>: <span class="tok-s">"457173"</span>, <span class="tok-k">"length"</span>: <span class="tok-n">6</span> },
  <span class="tok-k">"number"</span>: { <span class="tok-k">"length"</span>: <span class="tok-n">16</span>, <span class="tok-k">"luhn"</span>: <span class="tok-n">true</span> },
  <span class="tok-k">"scheme"</span>: <span class="tok-s">"visa"</span>,
  <span class="tok-k">"brand"</span>: <span class="tok-s">"Visa/Dankort"</span>,
  <span class="tok-k">"type"</span>: <span class="tok-s">"debit"</span>,
  <span class="tok-k">"level"</span>: <span class="tok-s">""</span>,
  <span class="tok-k">"prepaid"</span>: <span class="tok-n">null</span>,
  <span class="tok-k">"country"</span>: { <span class="tok-k">"alpha2"</span>: <span class="tok-s">"DK"</span>, <span class="tok-k">"name"</span>: <span class="tok-s">"Denmark"</span>, <span class="tok-k">"emoji"</span>: <span class="tok-s">"\U0001F1E9\U0001F1F0"</span> },
  <span class="tok-k">"bank"</span>: { <span class="tok-k">"name"</span>: <span class="tok-s">"Jyske Bank"</span>, <span class="tok-k">"url"</span>: <span class="tok-s">"www.jyskebank.dk"</span> }
}</code></pre>
      </div>
    </div>
    <p class="hint">Abbreviated for readability. See the <a href="/docs#schema">response schema</a> for every field.</p>
  </div>
</section>

<section class="section" id="data" style="padding-top:26px">
  <div class="wrap">
    <div class="reveal">
      <span class="section-tag">Open data</span>
      <h2>Where the data comes from</h2>
    </div>
    <div class="data-grid reveal">
      <div>
        <p class="lead" style="margin-bottom:18px">Guison is built on publicly available, openly licensed BIN data. The current primary dataset is
          <a href="https://github.com/{{.SourceRepository}}" target="_blank" rel="noopener noreferrer external">{{.SourceRepository}}</a>,
          published under CC BY 4.0.</p>
        <p style="color:var(--muted)">The service checks its configured sources for changes on a schedule. When a source changes, Guison downloads that exact version, validates it in a temporary table, and replaces that source's records in a single transaction. If any step fails, the previous data keeps serving.{{if .LastSynced}} The dataset was last synced on {{.LastSynced}}.{{end}}</p>
      </div>
      <div class="note">
        <strong>Accuracy disclaimer.</strong>
        <p style="margin:8px 0 0">BIN/IIN information may be incomplete, stale or inaccurate. This is community-maintained open data, not real-time payment network data. Do not treat Guison as authoritative payment verification, and never use it as a fraud or compliance control on its own.</p>
      </div>
    </div>
  </div>
</section>

<section class="section" id="community" style="padding-top:26px">
  <div class="wrap">
    <div class="tg-wrap reveal">
      <div>
        <div class="tg-icon" aria-hidden="true">{{template "tgicon" .}}</div>
        <h2 style="margin-bottom:10px">Join the Guison community</h2>
        <p style="color:var(--muted);margin:0;max-width:48ch">Follow the official channel for dataset updates and status notices, join the private community, or message the developer directly with integration questions.</p>
        {{if .UnsetCommunityVars}}
        <p class="tg-note">Setup required: set
          {{range $index, $name := .UnsetCommunityVars}}{{if $index}}, {{end}}<code>{{$name}}</code>{{end}}
          in the environment to activate the disabled {{if gt (len .UnsetCommunityVars) 1}}buttons{{else}}button{{end}}.</p>
        {{end}}
      </div>
      <div class="tg-actions">
        {{range .CommunityLinks}}
        {{if .Configured}}
        <a class="btn {{.Style}}" href="{{.URL | safeURL}}" target="_blank" rel="noopener noreferrer external">{{template "tgicon" .}} {{.Label}}</a>
        {{else}}
        <button class="btn {{.Style}}" type="button" disabled aria-disabled="true" title="Set {{.Placeholder}} to enable">{{template "tgicon" .}} {{.Label}}</button>
        {{end}}
        {{end}}
      </div>
    </div>
  </div>
</section>

<section class="section" id="faq" style="padding-top:26px">
  <div class="wrap">
    <div class="reveal">
      <span class="section-tag">Questions</span>
      <h2>Frequently asked</h2>
    </div>
    <div class="faq-layout">
      <div class="faq-list reveal">
        <details class="faq">
          <summary>What is Guison API?{{template "caret" .}}</summary>
          <div class="faq-body"><p>Guison is a public BIN/IIN lookup API. You send the first 6 to 8 digits of a payment card and it returns metadata about the issuing range: scheme, card type, level, country and bank, as JSON.</p></div>
        </details>
        <details class="faq">
          <summary>Is Guison free?{{template "caret" .}}</summary>
          <div class="faq-body"><p>Yes. The public lookup endpoints are free to use and require no account. Please be considerate with request volume so the service stays fast for everyone.</p></div>
        </details>
        <details class="faq">
          <summary>Do I need an API key?{{template "caret" .}}</summary>
          <div class="faq-body"><p>No. Public BIN lookup currently requires no API key and no authentication. Only the internal <code>/data</code> administration interface is protected.</p></div>
        </details>
        <details class="faq">
          <summary>What BIN lengths are supported?{{template "caret" .}}</summary>
          <div class="faq-body"><p>6, 7 or 8 digits. Anything shorter than 6 or longer than 8, or containing non-digits, returns <code>400 invalid_iin</code>. This is deliberate: it means a full card number can never be sent to Guison.</p></div>
        </details>
        <details class="faq">
          <summary>Where does the data come from?{{template "caret" .}}</summary>
          <div class="faq-body"><p>From openly licensed public datasets. The current primary source is <a href="https://github.com/{{.SourceRepository}}" target="_blank" rel="noopener noreferrer external">{{.SourceRepository}}</a>, licensed CC BY 4.0. Dataset credit is published here and in the project README; individual lookup responses stay lean and do not repeat it.</p></div>
        </details>
        <details class="faq">
          <summary>How often is data updated?{{template "caret" .}}</summary>
          <div class="faq-body"><p>Guison checks its configured sources on a schedule and imports a new version whenever the upstream file changes. Update frequency therefore depends on how often the upstream dataset is published{{if .LastSynced}}; the last successful sync was {{.LastSynced}}{{end}}.</p></div>
        </details>
        <details class="faq">
          <summary>Can Guison verify whether a card is valid?{{template "caret" .}}</summary>
          <div class="faq-body"><p><strong>No.</strong> Guison performs BIN/IIN metadata lookup only. It does not verify that a card or account exists, is active, has funds, or belongs to anyone. It cannot authorise, validate or check a payment card. Treat the data as informational only.</p></div>
        </details>
        <details class="faq">
          <summary>Why can some fields be null or empty?{{template "caret" .}}</summary>
          <div class="faq-body"><p>Because the underlying dataset does not have a value for them. Guison never guesses. Optional fields such as <code>prepaid</code>, <code>number.length</code> and <code>number.luhn</code> return <code>null</code>, and unknown strings return <code>""</code>.</p></div>
        </details>
        <details class="faq">
          <summary>What does /readyz mean?{{template "caret" .}}</summary>
          <div class="faq-body"><p><code>/readyz</code> reports whether a usable dataset is loaded. It returns <code>200</code> with <code>{"status":"ready"}</code> once lookups can be served, and <code>503 not_ready</code> while the first import is still running. <code>/health</code> only reports that the process is alive.</p></div>
        </details>
        <details class="faq">
          <summary>Can I use Guison in my project?{{template "caret" .}}</summary>
          <div class="faq-body"><p>Yes. Call it from a server or directly from a browser &mdash; CORS is open for the public routes. If you redistribute the data, keep the CC BY 4.0 attribution to the upstream dataset. Do not rely on Guison as your only source for anything that affects money or compliance.</p></div>
        </details>
      </div>
      <aside class="faq-art" aria-hidden="true">
        <img src="/assets/art/faq.webp" width="920" height="552" alt="" loading="lazy" decoding="async">
      </aside>
    </div>
  </div>
</section>

</main>
{{template "footer" .}}
{{end}}
`
