/* Guison API — site interactions.
   Vanilla JS, no dependencies. All API values are rendered with textContent. */
(function () {
  "use strict";

  var reduceMotion = window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;

  /* ---------- Sticky navbar ---------- */
  var nav = document.querySelector(".nav");
  if (nav) {
    var onScroll = function () {
      nav.classList.toggle("is-stuck", window.scrollY > 8);
    };
    onScroll();
    window.addEventListener("scroll", onScroll, { passive: true });
  }

  /* ---------- Mobile menu ---------- */
  var burger = document.querySelector(".burger");
  var links = document.getElementById("nav-links");
  if (burger && links) {
    burger.addEventListener("click", function () {
      var open = links.classList.toggle("open");
      burger.setAttribute("aria-expanded", open ? "true" : "false");
    });
    links.addEventListener("click", function (e) {
      if (e.target.closest("a")) {
        links.classList.remove("open");
        burger.setAttribute("aria-expanded", "false");
      }
    });
    document.addEventListener("keydown", function (e) {
      if (e.key === "Escape" && links.classList.contains("open")) {
        links.classList.remove("open");
        burger.setAttribute("aria-expanded", "false");
        burger.focus();
      }
    });
  }

  /* ---------- Scroll reveal ---------- */
  var reveals = document.querySelectorAll(".reveal");
  if (reveals.length) {
    if (reduceMotion || !("IntersectionObserver" in window)) {
      reveals.forEach(function (el) { el.classList.add("in"); });
    } else {
      var io = new IntersectionObserver(function (entries) {
        entries.forEach(function (entry) {
          if (entry.isIntersecting) {
            entry.target.classList.add("in");
            io.unobserve(entry.target);
          }
        });
      }, { rootMargin: "0px 0px -8% 0px", threshold: 0.06 });
      reveals.forEach(function (el) { io.observe(el); });
    }
  }

  /* ---------- Copy buttons ---------- */
  function flash(button, message) {
    var original = button.getAttribute("data-label") || button.textContent;
    button.setAttribute("data-label", original);
    button.textContent = message;
    button.classList.add("copied");
    window.setTimeout(function () {
      button.textContent = original;
      button.classList.remove("copied");
    }, 1500);
  }

  function copyText(text, button) {
    var done = function () { flash(button, "Copied"); };
    var fail = function () { flash(button, "Press Ctrl+C"); };
    if (navigator.clipboard && window.isSecureContext) {
      navigator.clipboard.writeText(text).then(done).catch(fail);
      return;
    }
    try {
      var area = document.createElement("textarea");
      area.value = text;
      area.setAttribute("readonly", "");
      area.style.position = "fixed";
      area.style.opacity = "0";
      document.body.appendChild(area);
      area.select();
      var ok = document.execCommand("copy");
      document.body.removeChild(area);
      ok ? done() : fail();
    } catch (err) {
      fail();
    }
  }

  document.addEventListener("click", function (event) {
    var button = event.target.closest("[data-copy]");
    if (!button) return;
    var selector = button.getAttribute("data-copy");
    var source = selector ? document.querySelector(selector) : null;
    if (source) copyText(source.textContent, button);
  });

  /* ---------- Example chips ---------- */
  var binInput = document.getElementById("bin-input");
  document.querySelectorAll("[data-example]").forEach(function (chip) {
    chip.addEventListener("click", function () {
      if (!binInput) return;
      binInput.value = chip.getAttribute("data-example");
      binInput.focus();
    });
  });

  /* ---------- Language tabs (docs) ---------- */
  document.querySelectorAll("[data-tabs]").forEach(function (group) {
    var tabs = group.querySelectorAll(".tab");
    tabs.forEach(function (tab) {
      tab.addEventListener("click", function () {
        var target = tab.getAttribute("data-panel");
        tabs.forEach(function (other) {
          var selected = other === tab;
          other.setAttribute("aria-selected", selected ? "true" : "false");
          other.setAttribute("tabindex", selected ? "0" : "-1");
        });
        group.parentNode.querySelectorAll(".panel").forEach(function (panel) {
          panel.hidden = panel.id !== target;
        });
      });
      tab.addEventListener("keydown", function (event) {
        if (event.key !== "ArrowRight" && event.key !== "ArrowLeft") return;
        event.preventDefault();
        var list = Array.prototype.slice.call(tabs);
        var index = list.indexOf(tab);
        var next = event.key === "ArrowRight" ? (index + 1) % list.length : (index - 1 + list.length) % list.length;
        list[next].focus();
        list[next].click();
      });
    });
  });

  /* ---------- Docs table of contents ---------- */
  var tocLinks = document.querySelectorAll(".toc a");
  if (tocLinks.length && "IntersectionObserver" in window) {
    var sections = [];
    tocLinks.forEach(function (link) {
      var section = document.querySelector(link.getAttribute("href"));
      if (section) sections.push({ link: link, section: section });
    });
    var spy = new IntersectionObserver(function (entries) {
      entries.forEach(function (entry) {
        if (!entry.isIntersecting) return;
        tocLinks.forEach(function (link) { link.classList.remove("active"); });
        var match = sections.find(function (item) { return item.section === entry.target; });
        if (match) match.link.classList.add("active");
      });
    }, { rootMargin: "-88px 0px -72% 0px" });
    sections.forEach(function (item) { spy.observe(item.section); });
  }

  /* ---------- Status check ---------- */
  var statusEl = document.getElementById("status");
  if (statusEl) {
    var label = statusEl.querySelector(".status-text");

    var setStatus = function (state, text) {
      statusEl.classList.remove("ok", "warn", "down");
      if (state) statusEl.classList.add(state);
      if (label) label.textContent = text;
    };

    var checkStatus = function () {
      setStatus("", "Checking status\u2026");
      fetch("/health", { headers: { Accept: "text/plain" } })
        .then(function (response) {
          if (!response.ok) throw new Error("health");
          return fetch("/readyz", { headers: { Accept: "application/json" } });
        })
        .then(function (response) {
          if (response.ok) setStatus("ok", "API operational");
          else setStatus("warn", "Online \u2014 dataset loading");
        })
        .catch(function () { setStatus("down", "Status unavailable"); });
    };

    checkStatus();
    var refresh = statusEl.querySelector(".status-refresh");
    if (refresh) refresh.addEventListener("click", checkStatus);
  }

  /* ---------- Live BIN tester ---------- */
  var form = document.getElementById("tester");
  if (!form) return;

  var result = document.getElementById("result");
  var button = document.getElementById("lookup-btn");
  var buttonLabel = button.querySelector(".btn-label");
  var busy = false;

  function clearNode(node) {
    while (node.firstChild) node.removeChild(node.firstChild);
  }

  function element(tag, className, text) {
    var node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined && text !== null) node.textContent = text;
    return node;
  }

  function showAlert(kind, title, detail) {
    clearNode(result);
    var box = element("div", "alert " + (kind === "warn" ? "alert-warn" : "alert-err"));
    var body = element("div");
    body.appendChild(element("strong", null, title));
    if (detail) {
      body.appendChild(document.createElement("br"));
      body.appendChild(element("span", null, detail));
    }
    box.appendChild(body);
    result.appendChild(box);
    result.hidden = false;
  }

  function present(value) {
    if (value === null || value === undefined) return null;
    if (typeof value === "string" && value.trim() === "") return null;
    if (typeof value === "boolean") return value ? "Yes" : "No";
    return String(value);
  }

  function renderResult(data) {
    clearNode(result);

    var match = data.match || {};
    var country = data.country || {};
    var bank = data.bank || {};
    var number = data.number || {};

    var countryText = null;
    if (present(country.name)) {
      countryText = (present(country.emoji) ? country.emoji + " " : "") + country.name;
    } else if (present(country.alpha2)) {
      countryText = country.alpha2;
    }

    var rangeText = null;
    if (present(match.start)) {
      rangeText = match.start === match.end ? match.start : match.start + " \u2013 " + match.end;
    }

    var fields = [
      ["IIN", present(data.iin)],
      ["Scheme", present(data.scheme)],
      ["Brand", present(data.brand)],
      ["Type", present(data.type)],
      ["Level", present(data.level)],
      ["Prepaid", present(data.prepaid)],
      ["Country", countryText],
      ["Currency", present(country.currency)],
      ["Bank", present(bank.name)],
      ["Bank phone", present(bank.phone)],
      ["Bank site", present(bank.url)],
      ["PAN length", present(number.length)],
      ["Luhn", present(number.luhn)],
      ["Matched range", rangeText]
    ];

    var grid = element("div", "result-grid");
    fields.forEach(function (pair) {
      var card = element("div", "rcard");
      card.appendChild(element("span", "k", pair[0]));
      if (pair[1] === null) card.appendChild(element("span", "v null", "null"));
      else card.appendChild(element("span", "v", pair[1]));
      grid.appendChild(card);
    });
    result.appendChild(grid);

    var bar = element("div", "result-bar");

    var pretty = JSON.stringify(data, null, 2);
    var rawBlock = element("div", "code");
    rawBlock.hidden = true;
    rawBlock.style.marginTop = "14px";
    var head = element("div", "code-head");
    head.appendChild(element("span", "code-title", "Raw JSON response"));
    var copyRaw = element("button", "mini", "Copy JSON");
    copyRaw.type = "button";
    rawBlock.appendChild(head);
    var pre = document.createElement("pre");
    var codeEl = element("code", null, pretty);
    pre.appendChild(codeEl);
    rawBlock.appendChild(pre);
    head.appendChild(copyRaw);

    var toggle = element("button", "mini", "View Raw JSON");
    toggle.type = "button";
    toggle.setAttribute("aria-expanded", "false");
    toggle.addEventListener("click", function () {
      rawBlock.hidden = !rawBlock.hidden;
      toggle.setAttribute("aria-expanded", rawBlock.hidden ? "false" : "true");
      toggle.textContent = rawBlock.hidden ? "View Raw JSON" : "Hide Raw JSON";
    });

    var copyTop = element("button", "mini", "Copy JSON");
    copyTop.type = "button";
    var doCopy = function (btn) { return function () { copyText(pretty, btn); }; };
    copyTop.addEventListener("click", doCopy(copyTop));
    copyRaw.addEventListener("click", doCopy(copyRaw));

    bar.appendChild(toggle);
    bar.appendChild(copyTop);
    result.appendChild(bar);
    result.appendChild(rawBlock);
    result.hidden = false;
  }

  function setBusy(state) {
    busy = state;
    button.disabled = state;
    clearNode(button);
    if (state) {
      button.appendChild(element("span", "spinner"));
      button.appendChild(element("span", "btn-label", "Looking up"));
    } else {
      button.appendChild(element("span", "btn-label", "Lookup"));
    }
  }

  form.addEventListener("submit", function (event) {
    event.preventDefault();
    if (busy) return;

    var value = (binInput.value || "").trim();

    if (!/^[0-9]+$/.test(value)) {
      showAlert("warn", "Digits only", "Enter a 6 to 8 digit BIN/IIN, for example 45717360.");
      binInput.focus();
      return;
    }
    if (value.length > 8) {
      showAlert("warn", "That looks too long", "Guison accepts only the first 6 to 8 digits of a card. Never enter a full card number.");
      binInput.focus();
      return;
    }
    if (value.length < 6) {
      showAlert("warn", "Too short", "A BIN/IIN lookup needs at least 6 digits.");
      binInput.focus();
      return;
    }

    setBusy(true);
    clearNode(result);
    result.hidden = true;

    fetch("/" + encodeURIComponent(value), { headers: { Accept: "application/json" } })
      .then(function (response) {
        return response.json().then(function (body) {
          return { status: response.status, body: body };
        });
      })
      .then(function (payload) {
        if (payload.status === 200) {
          renderResult(payload.body);
          return;
        }
        var err = (payload.body && payload.body.error) || {};
        if (payload.status === 404) {
          showAlert("warn", "No record found", "No dataset entry covers " + value + ". The BIN may be unallocated or missing from the current dataset.");
        } else if (payload.status === 400) {
          showAlert("warn", "Invalid input", err.message || "Enter 6 to 8 digits.");
        } else if (payload.status === 503) {
          showAlert("err", "Dataset not ready", "The service is running but has no usable dataset yet. Try again shortly.");
        } else {
          showAlert("err", "Lookup failed", err.message || ("The server returned HTTP " + payload.status + "."));
        }
      })
      .catch(function () {
        showAlert("err", "Network error", "Could not reach the API. Check your connection and try again.");
      })
      .then(function () { setBusy(false); });
  });
})();
