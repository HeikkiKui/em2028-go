"use strict";
(() => {
  // Kaikki käyttäjän syöttämä sisältö asetetaan textContentin kautta (ei innerHTML:ää), joten XSS ei ole mahdollinen.
  const GC = { A: "var(--A)", B: "var(--B)", C: "var(--C)", D: "var(--D)", E: "var(--E)", F: "var(--F)" };
  const state = {
    me: null, tab: "predict", sub: "groups", group: "A", data: null, scorer: null, timer: null,
    drafts: {}, saveTimers: {},
  };
  const app = document.getElementById("app");
  const tabsEl = document.getElementById("tabs");

  /* ---------- apurit ---------- */
  function h(tag, props, ...kids) {
    const el = document.createElement(tag);
    for (const [k, v] of Object.entries(props || {})) {
      if (v === false || v == null) continue;
      if (k === "class") el.className = v;
      else if (k === "value") el.value = v;
      else if (k === "checked") el.checked = !!v;
      else if (k === "disabled") el.disabled = !!v;
      else if (k === "hidden") el.hidden = !!v;
      else if (k === "cssVars") for (const [n, val] of Object.entries(v)) el.style.setProperty(n, val);
      else if (k.startsWith("on") && typeof v === "function") el.addEventListener(k.slice(2), v);
      else el.setAttribute(k, v === true ? "" : String(v));
    }
    for (const kid of kids.flat(Infinity)) {
      if (kid == null || kid === false) continue;
      el.append(kid.nodeType ? kid : document.createTextNode(String(kid)));
    }
    return el;
  }
  const clearNode = (n) => { while (n.firstChild) n.removeChild(n.firstChild); };
  const stopTimer = () => { if (state.timer) { clearInterval(state.timer); state.timer = null; } };

  async function api(method, path, body) {
    const opts = { method, credentials: "same-origin", headers: { "X-Requested-With": "em2028" } };
    if (body !== undefined) {
      opts.headers["Content-Type"] = "application/json";
      opts.body = JSON.stringify(body);
    }
    let res;
    try { res = await fetch("/api" + path, opts); }
    catch { throw Object.assign(new Error("Yhteys palvelimeen katkesi"), { status: 0 }); }
    let data = null;
    if (res.status !== 204) { try { data = await res.json(); } catch { /* tyhjä vastaus */ } }
    if (!res.ok) throw Object.assign(new Error((data && data.error) || "Jokin meni pieleen"), { status: res.status });
    return data;
  }
  function toLogin() { stopTimer(); state.me = null; state.data = null; state.player = null; renderAuth(); }

  const fmtDay = new Intl.DateTimeFormat("fi-FI", { weekday: "short", day: "numeric", month: "numeric", timeZone: "UTC" });
  const fmtFull = new Intl.DateTimeFormat("fi-FI", { weekday: "short", day: "numeric", month: "numeric", hour: "2-digit", minute: "2-digit" });
  const fmtClock = new Intl.DateTimeFormat("fi-FI", { hour: "2-digit", minute: "2-digit" });
  function kickoffText(m) {
    const d = new Date(m.kickoff * 1000);
    return m.kickoffConfirmed ? fmtFull.format(d) : fmtDay.format(d) + ", kellonaika vahvistuu";
  }
  function countdown(ms) {
    if (ms <= 0) return "alkamassa";
    const min = Math.floor(ms / 60000), d = Math.floor(min / 1440), hh = Math.floor((min % 1440) / 60), mm = min % 60;
    return d > 0 ? d + " pv " + hh + " h" : hh > 0 ? hh + " h " + mm + " min" : mm + " min";
  }
  function slotLabel(s) {
    if (!s) return "Selviää myöhemmin";
    if (/^[12][A-F]$/.test(s)) return s[0] === "1" ? "Lohkon " + s[1] + " voittaja" : "Lohkon " + s[1] + " kakkonen";
    if (s.startsWith("T:")) return "Kolmonen (" + s.slice(2).split("").join("/") + ")";
    if (s[0] === "W") return "Ottelun " + s.slice(1) + " voittaja";
    return s;
  }
  const lsGet = (k) => { try { return localStorage.getItem(k); } catch { return null; } };
  const lsSet = (k, v) => { try { localStorage.setItem(k, v); } catch { /* ei väliä */ } };
  const teamName = (t) => (t ? t.name || t.code : "");
  const deltaMark = (d) => (d == null || d === 0 ? null : h("small", { class: d > 0 ? "up" : "down" }, (d > 0 ? " ▲" : " ▼") + Math.abs(d)));

  /* ---------- kirjautuminen ---------- */
  function field(id, label, input, hint) {
    return h("div", { class: "field" }, h("label", { for: id }, label), input, hint ? h("span", { class: "note" }, hint) : null);
  }
  // Salasanakenttä, jossa on Näytä/Piilota-painike. Painike ei vie kentältä kohdistusta (näppäimistö pysyy auki).
  function passwordInput(id, autocomplete, extra) {
    const input = h("input", Object.assign({ type: "password", id, autocomplete, maxlength: 128 }, extra || {}));
    const toggle = h("button", { class: "btn small", type: "button", "aria-pressed": "false", "aria-controls": id, "aria-label": "Näytä salasana" }, "Näytä");
    toggle.addEventListener("mousedown", (e) => e.preventDefault());
    toggle.addEventListener("click", () => {
      const show = input.type === "password";
      input.type = show ? "text" : "password";
      toggle.textContent = show ? "Piilota" : "Näytä";
      toggle.setAttribute("aria-pressed", String(show));
      toggle.setAttribute("aria-label", show ? "Piilota salasana" : "Näytä salasana");
    });
    return { input, node: h("div", { class: "pw" }, input, toggle) };
  }
  function strength(pw) {
    let score = Math.min(pw.length, 20) * 4;
    if (/[a-zåäö]/.test(pw) && /[A-ZÅÄÖ]/.test(pw)) score += 6;
    if (/\d/.test(pw)) score += 5;
    if (/[^A-Za-z0-9åäöÅÄÖ]/.test(pw)) score += 6;
    if (pw.length < 10) score = Math.min(score, 30);
    return Math.max(0, Math.min(100, score));
  }

  function renderAuth(mode = "login") {
    stopTimer();
    tabsEl.hidden = true;
    clearNode(app);
    const seg = h("div", { class: "seg", role: "tablist" },
      h("button", { type: "button", role: "tab", "aria-selected": String(mode === "login"), onclick: () => renderAuth("login") }, "Kirjaudu"),
      h("button", { type: "button", role: "tab", "aria-selected": String(mode === "register"), onclick: () => renderAuth("register") }, "Luo tunnus"));
    app.append(h("section", { class: "view" },
      h("h2", null, mode === "register" ? "Luo tunnus" : "Kirjaudu"),
      h("p", { class: "lead" }, mode === "register"
        ? "Veikkaa koko turnaus ennen ensimmäistä ottelua: lohkot, pudotuspelit ja maalikuningas."
        : "Kirjaudu sisään jatkaaksesi veikkaamista."),
      seg, h("div", { class: "card" }, mode === "register" ? registerForm() : loginForm())));
  }

  function loginForm() {
    const email = h("input", { type: "email", id: "email", autocomplete: "username", maxlength: 254, inputmode: "email" });
    const pw = passwordInput("pw", "current-password");
    const err = h("div", { class: "err", role: "alert" });
    const btn = h("button", { class: "btn primary", type: "button" }, "Kirjaudu");
    const go = async () => {
      err.textContent = ""; btn.disabled = true;
      try {
        const r = await api("POST", "/login", { email: email.value, password: pw.input.value });
        state.me = { nickname: r.nickname, emailVerified: r.emailVerified };
        pw.input.value = "";
        await start();
      } catch (e) { err.textContent = e.message; btn.disabled = false; }
    };
    btn.addEventListener("click", go);
    for (const i of [email, pw.input]) i.addEventListener("keydown", (e) => { if (e.key === "Enter") go(); });
    queueMicrotask(() => email.focus());
    return [field("email", "Sähköposti", email), field("pw", "Salasana", pw.node), err,
      h("div", { class: "row" }, btn, h("button", { class: "linkbtn", type: "button", onclick: () => renderForgot() }, "Unohtuiko salasana?"))];
  }

  function registerForm() {
    const nick = h("input", { type: "text", id: "nick", maxlength: 20, autocomplete: "nickname" });
    const email = h("input", { type: "email", id: "email", autocomplete: "email", maxlength: 254, inputmode: "email" });
    const pw = passwordInput("pw", "new-password");
    const fill = h("i");
    pw.input.addEventListener("input", () => fill.style.setProperty("--w", strength(pw.input.value) + "%"));
    const err = h("div", { class: "err", role: "alert" });
    const btn = h("button", { class: "btn primary", type: "button" }, "Luo tunnus");
    const go = async () => {
      err.textContent = ""; btn.disabled = true;
      try {
        const r = await api("POST", "/register", { nickname: nick.value, email: email.value, password: pw.input.value });
        state.me = { nickname: r.nickname, emailVerified: false };
        pw.input.value = "";
        await start();
      } catch (e) { err.textContent = e.message; btn.disabled = false; }
    };
    btn.addEventListener("click", go);
    for (const i of [nick, email, pw.input]) i.addEventListener("keydown", (e) => { if (e.key === "Enter") go(); });
    queueMicrotask(() => nick.focus());
    return [
      field("nick", "Nimimerkki", nick, "Näkyy muille pelaajille."),
      field("email", "Sähköposti", email, "Vain vahvistukseen ja salasanan palautukseen. Ei näy muille."),
      field("pw", "Salasana", pw.node, "Vähintään 10 merkkiä. Pitkä lause on parempi kuin monimutkainen sana."),
      h("div", { class: "meter", "aria-hidden": "true" }, fill),
      h("div", { class: "note" }, "Luomalla tunnuksen hyväksyt ", h("a", { href: "privacy.html", target: "_blank", rel: "noopener" }, "tietosuojaselosteen"), "."),
      err, h("div", { class: "row" }, btn)];
  }

  function renderForgot() {
    stopTimer();
    clearNode(app);
    const email = h("input", { type: "email", id: "email", autocomplete: "email", maxlength: 254, inputmode: "email" });
    const msg = h("div", { role: "status" });
    const btn = h("button", { class: "btn primary", type: "button" }, "Lähetä palautuslinkki");
    const go = async () => {
      msg.className = ""; msg.textContent = ""; btn.disabled = true;
      try {
        await api("POST", "/password/forgot", { email: email.value });
        msg.className = "okmsg";
        msg.textContent = "Jos osoite on rekisteröity, lähetimme siihen linkin. Se on voimassa tunnin. Tarkista myös roskaposti.";
      } catch (e) { msg.className = "err"; msg.textContent = e.message; }
      btn.disabled = false;
    };
    btn.addEventListener("click", go);
    email.addEventListener("keydown", (e) => { if (e.key === "Enter") go(); });
    app.append(h("section", { class: "view" },
      h("h2", null, "Salasanan palautus"),
      h("p", { class: "lead" }, "Anna tunnukseesi liittyvä sähköposti, niin lähetämme linkin uuden salasanan valintaan."),
      h("div", { class: "card" }, field("email", "Sähköposti", email), msg,
        h("div", { class: "row" }, btn, h("button", { class: "linkbtn", type: "button", onclick: () => renderAuth("login") }, "Takaisin kirjautumiseen")))));
    email.focus();
  }

  /* ---------- sovelluksen runko ---------- */
  async function loadData() {
    const [d, s] = await Promise.all([api("GET", "/bracket"), api("GET", "/scorer")]);
    state.data = d;
    state.scorer = s;
  }
  async function start() {
    try { state.me = await api("GET", "/me"); await loadData(); render(); }
    catch (e) {
      if (e.status === 401) { toLogin(); return; }
      clearNode(app); tabsEl.hidden = true; app.append(h("p", { class: "err" }, e.message));
    }
  }
  async function refresh(justSaved) {
    try {
      state.data = await api("GET", "/bracket");
      // Tallennetut luonnokset korvataan palvelimen tiedoilla (kaavio voi muuttua), paitsi juuri tallennetun tilaviesti.
      for (const k of Object.keys(state.drafts)) if (!state.drafts[k].dirty && +k !== justSaved) delete state.drafts[k];
      render();
    }
    catch (e) { if (e.status === 401) toLogin(); }
  }

  const TABS = [["predict", "Veikkaukset"], ["board", "Tulostaulu"], ["chat", "Chat"], ["account", "Tili"]];

  function verifyNotice() {
    if (!state.me || state.me.emailVerified) return null;
    const msg = h("span", { role: "status" });
    const btn = h("button", { class: "btn small", type: "button" }, "Lähetä uudelleen");
    btn.addEventListener("click", async () => {
      btn.disabled = true;
      try { await api("POST", "/email/resend"); msg.className = "okmsg"; msg.textContent = " Lähetetty."; }
      catch (e) { msg.className = "err"; msg.textContent = " " + e.message; btn.disabled = false; }
    });
    return h("div", { class: "notice" }, "Vahvista sähköpostisi: lähetimme sinulle linkin. ", btn, msg);
  }

  function render() {
    const ae = document.activeElement;
    const fid = ae && ae.dataset ? ae.dataset.fid : null;
    const caret = fid && ae.selectionStart != null ? ae.selectionStart : null;
    stopTimer();
    tabsEl.hidden = false;
    clearNode(tabsEl);
    for (const [id, label] of TABS) {
      tabsEl.append(h("button", {
        role: "tab", "aria-selected": String(state.tab === id), type: "button",
        onclick: () => { state.tab = id; state.player = null; render(); window.scrollTo(0, 0); },
      }, label));
    }
    clearNode(app);
    const view = state.player ? viewPlayerPage() : { predict: viewPredict, board: viewBoard, chat: viewChat, account: viewAccount }[state.tab]();
    app.append(h("section", { class: "view" }, state.tab === "chat" || state.tab === "account" ? verifyNotice() : null, view));
    if (fid) {
      const el = app.querySelector('[data-fid="' + fid + '"]');
      if (el && !el.disabled) { el.focus({ preventScroll: true }); if (caret != null && el.setSelectionRange) el.setSelectionRange(caret, caret); }
    }
  }

  /* ---------- veikkaukset ---------- */
  function banner(d) {
    const picked = !!(state.scorer && state.scorer.player);
    const pct = Math.round(((d.predicted + (picked ? 1 : 0)) / (d.total + 1)) * 100);
    const bar = h("div", { class: "progressbar", role: "progressbar", "aria-valuemin": "0", "aria-valuemax": "100", "aria-valuenow": String(pct), "aria-label": "Veikkausten edistyminen" },
      h("i", { cssVars: { "--w": pct + "%" } }));
    const progress = h("span", { class: "progress" }, "Veikattu " + d.predicted + " / " + d.total + " ottelua" + (picked ? ", maalikuningas valittu" : ""));
    if (d.locked) return h("div", { class: "banner locked" }, h("b", null, "Veikkaukset on lukittu."), " Turnaus on alkanut.", bar, progress);
    const when = new Date(d.lockAt * 1000);
    return h("div", { class: "banner" }, h("b", null, "Veikkaukset auki"),
      " – sulkeutuvat " + fmtDay.format(when) + (d.lockConfirmed ? " klo " + fmtClock.format(when) : "") + " (" + countdown(when.getTime() - Date.now()) + ")", bar, progress);
  }

  // Ensikertalaisen lyhyt opas (piilotettavissa).
  function guide(d) {
    if (d.locked || d.readonly || d.predicted > 0 || lsGet("em2028-guide") === "1") return null;
    return h("div", { class: "guide" },
      h("span", null, "Kirjoita jokaiselle ottelulle tulos, se tallentuu itsestään. Voit muuttaa kaikkea, kunnes turnaus alkaa. "),
      h("button", { class: "btn small", type: "button", onclick: () => { lsSet("em2028-guide", "1"); render(); } }, "Selvä"));
  }

  // Kolme vaihetta kerralla: selkeä järjestys ja merkki valmiista vaiheista.
  function stepsBar(d) {
    const groupDone = d.matches.filter((m) => m.stage === "group" && m.prediction).length;
    const koDone = d.matches.filter((m) => m.stage !== "group" && m.prediction).length;
    const picked = !!(state.scorer && state.scorer.player);
    const steps = [
      ["groups", "1", "Lohkot", groupDone + "/36", groupDone === 36],
      ["ko", "2", "Pudotuspelit", koDone + "/15", koDone === 15],
      ["scorer", "3", "Maalikuningas", picked ? "valittu" : "puuttuu", picked],
    ];
    return h("div", { class: "steps", role: "tablist", "aria-label": "Veikkauksen vaiheet" }, steps.map(([id, n, label, status, done]) =>
      h("button", { type: "button", role: "tab", class: "step" + (done ? " done" : ""), "aria-selected": String(state.sub === id),
        onclick: () => { state.sub = id; render(); window.scrollTo(0, 0); } },
        h("span", { class: "num" }, done ? "✓" : n), h("span", { class: "lbl" }, label), h("small", null, status))));
  }

  function viewPredict() {
    const d = state.data;
    if (!d.locked) state.timer = setInterval(() => { if (!document.hidden && !Object.values(state.drafts).some((x) => x.dirty)) render(); }, 60000);
    if (state.sub === "tree") state.sub = "ko"; // vanha arvo
    const body = state.sub === "groups" ? viewGroups(d) : state.sub === "ko" ? viewKnockout(d) : viewScorer();
    return [stepsBar(d), banner(d), guide(d), body];
  }

  // Satunnaiset tulokset puuttuviin otteluihin: ei koske jo veikattuihin.
  const rndGoals = () => { const x = Math.random(); return x < 0.3 ? 0 : x < 0.65 ? 1 : x < 0.88 ? 2 : x < 0.97 ? 3 : 4; };
  async function randomFill(g, btn, msg) {
    btn.disabled = true; msg.textContent = "";
    try {
      for (const m of state.data.matches.filter((x) => x.group === g && !x.prediction)) {
        await api("PUT", "/predictions/" + m.n, { homeGoals: rndGoals(), awayGoals: rndGoals(), advance: "" });
      }
      await refresh();
    } catch (e) {
      if (e.status === 401) { toLogin(); return; }
      msg.className = "err"; msg.textContent = e.message; btn.disabled = false;
    }
  }

  function viewGroups(d) {
    const g = state.group;
    const letters = Object.keys(GC);
    const chips = h("div", { class: "gchips", role: "group", "aria-label": "Valitse lohko" },
      letters.map((x) => {
        const n = d.matches.filter((m) => m.group === x && m.prediction).length;
        return h("button", { class: "gchip", type: "button", cssVars: { "--gc": GC[x] }, "aria-pressed": String(x === g),
          "aria-label": "Lohko " + x + ", " + n + " / 6 veikattu", onclick: () => { state.group = x; render(); } },
          x, h("small", null, n + "/6"));
      }));

    // Lohkon sijoitukset veikkauksista (puhelimella ottelujen alla)
    const grp = d.groups[g];
    const rows = grp.table.map((t, i) => h("tr", { class: i < 2 ? "adv-row" : i === 2 ? "third-row" : "" },
      h("td", { class: "pos" }, i + 1),
      h("td", { class: "l" }, t.name && t.name !== t.code ? [t.name, " ", h("span", { class: "code" }, t.code)] : t.code),
      h("td", null, t.played), h("td", null, t.gf + "–" + t.ga), h("td", { class: "pts-cell" }, t.points)));
    const rmsg = h("span", { role: "status" });
    const rbtn = h("button", { class: "btn small", type: "button", disabled: d.locked }, "Täytä puuttuvat satunnaisesti");
    rbtn.addEventListener("click", () => randomFill(g, rbtn, rmsg));
    const left = h("div", null,
      h("div", { class: "panel", cssVars: { "--gc": GC[g] } },
        h("h3", null, "Lohko " + g + ": sijoitukset"),
        h("div", { class: "scroll" }, h("table", null,
          h("thead", null, h("tr", null, h("th", null, ""), h("th", { class: "l" }, "Joukkue"), h("th", null, "Pelit"), h("th", null, "Maalit"), h("th", null, "Pisteet"))),
          h("tbody", null, rows))),
        h("p", { class: "note" }, "Kaksi parasta jatkaa pudotuspeleihin, lisäksi neljä parasta kolmosta. Taulukko päivittyy veikkauksistasi.")),
      d.readonly ? null : h("div", { class: "row" }, rbtn, rmsg));

    // Ottelut kierroksittain + seuraava-painike
    const right = h("div", null);
    [1, 2, 3].forEach((md) => {
      right.append(h("div", { class: "md" }, md + ". kierros"));
      for (const m of d.matches.filter((x) => x.group === g && Math.ceil(x.n / 12) === md)) right.append(predictCard(m, d));
    });
    if (!d.readonly) {
      const idx = letters.indexOf(g);
      const done = d.matches.filter((m) => m.group === g && m.prediction).length;
      const go = idx < 5
        ? h("button", { class: "btn primary next", type: "button", onclick: () => { state.group = letters[idx + 1]; render(); window.scrollTo(0, 0); } }, "Seuraava: Lohko " + letters[idx + 1] + " →")
        : h("button", { class: "btn primary next", type: "button", onclick: () => { state.sub = "ko"; render(); window.scrollTo(0, 0); } }, "Pudotuspeleihin →");
      right.append(h("div", { class: "nextrow" }, done < 6 ? h("p", { class: "note" }, (6 - done) + " ottelua puuttuu tästä lohkosta.") : null, go));
    }
    return [chips, h("div", { class: "cols" }, left, right)];
  }

  const ROUNDS = [["Kahdeksannesvälierät", 37, 44], ["Neljännesvälierät", 45, 48], ["Välierät", 49, 50], ["Finaali", 51, 51]];
  function viewKnockout(d) {
    const groupDone = d.matches.filter((m) => m.stage === "group" && m.prediction).length;
    if (!d.readonly && !d.locked && groupDone < 36) {
      return [h("div", { class: "card" },
        h("h3", null, "Veikkaa ensin lohkot"),
        h("p", { class: "lead" }, "Pudotuspelien ottelut muodostuvat lohkoveikkauksistasi. Veikkaa vielä " + (36 - groupDone) + " lohkopeliä, niin pudotuspelit avautuvat tähän."),
        h("button", { class: "btn primary next", type: "button", onclick: () => { state.sub = "groups"; render(); window.scrollTo(0, 0); } }, "Jatka lohkoihin"))];
    }
    const champ = d.champion;
    const shareMsg = h("span", { class: "okmsg", role: "status" });
    const shareBtn = champ && !d.readonly
      ? h("button", { class: "btn small", type: "button", onclick: async () => { shareMsg.textContent = await shareBracket(); } }, "Jaa veikkaukseni")
      : null;
    const out = [h("div", { class: "champion" },
      champ ? [h("span", { class: "note" }, d.readonly ? "Mestari" : "Mestarisi"), h("b", null, teamName(champ)), shareBtn, shareMsg]
        : h("span", { class: "note" }, "Veikkaa pudotuspelit finaaliin asti, niin näet mestarisi tässä."))];
    out.push(h("div", { class: "seg", role: "tablist" }, [["list", "Lista"], ["tree", "Kaavio"]].map(([id, label]) => h("button", {
      type: "button", role: "tab", "aria-selected": String((state.koMode || "list") === id), onclick: () => { state.koMode = id; render(); },
    }, label))));
    if (state.koMode === "tree") {
      out.push(viewTree(d));
    } else {
      for (const [name, a, b] of ROUNDS) {
        out.push(h("h3", null, name), d.matches.filter((m) => m.n >= a && m.n <= b).map((m) => predictCard(m, d)));
      }
    }
    if (!d.readonly) {
      out.push(h("div", { class: "nextrow" }, h("button", { class: "btn primary next", type: "button",
        onclick: () => { state.sub = "scorer"; render(); window.scrollTo(0, 0); } }, "Seuraava: Maalikuningas →")));
    }
    return out;
  }

  function currentOf(m, d) {
    const dr = d.readonly ? null : state.drafts[m.n];
    if (dr && dr.dirty) return dr;
    if (m.prediction) return { h: String(m.prediction.homeGoals), a: String(m.prediction.awayGoals), adv: m.prediction.advance || "", status: dr ? dr.status : "" };
    return { h: "", a: "", adv: "", status: dr ? dr.status : "" };
  }

  function scheduleSave(m) {
    clearTimeout(state.saveTimers[m.n]);
    state.saveTimers[m.n] = setTimeout(async () => {
      const dr = state.drafts[m.n];
      if (!dr || !dr.dirty) return;
      const isKO = m.stage !== "group";
      const tie = dr.h !== "" && dr.a !== "" && +dr.h === +dr.a;
      if (dr.h === "" || dr.a === "") return;
      if (isKO && tie && !dr.adv) { dr.status = "Valitse jatkoon selviävä joukkue"; render(); return; }
      const snapshot = { h: dr.h, a: dr.a, adv: dr.adv };
      try {
        await api("PUT", "/predictions/" + m.n, { homeGoals: +snapshot.h, awayGoals: +snapshot.a, advance: isKO && tie ? snapshot.adv : "" });
        const now = state.drafts[m.n];
        if (now && now.h === snapshot.h && now.a === snapshot.a && now.adv === snapshot.adv) { now.dirty = false; now.status = "Tallennettu"; }
        await refresh(m.n);
      } catch (e) {
        if (e.status === 401) { toLogin(); return; }
        dr.status = e.message; render();
      }
    }, 450);
  }

  function predictCard(m, d) {
    const isKO = m.stage !== "group";
    const known = !!(m.home && m.away);
    const disabled = d.locked || !known;
    const cur = currentOf(m, d);
    const tie = cur.h !== "" && cur.a !== "" && +cur.h === +cur.a;
    const homeName = m.home ? teamName(m.home) : slotLabel(m.homeSlot);
    const awayName = m.away ? teamName(m.away) : slotLabel(m.awaySlot);
    const edit = (key) => (e) => {
      e.target.value = e.target.value.replace(/\D/g, "").slice(0, 2);
      const dr = state.drafts[m.n] && state.drafts[m.n].dirty ? state.drafts[m.n] : Object.assign({}, cur);
      dr[key] = e.target.value; dr.dirty = true; dr.status = "";
      state.drafts[m.n] = dr;
      scheduleSave(m);
      if (e.target.value !== "") { // seuraavaan kenttään, jotta tulokset saa syötettyä nopeasti peräkkäin
        const all = [...app.querySelectorAll(".sc input:not([disabled])")];
        const i = all.indexOf(e.target);
        if (i >= 0 && i < all.length - 1) all[i + 1].focus();
      }
    };
    const mk = (key, label) => h("input", { type: "text", inputmode: "numeric", maxlength: 2, autocomplete: "off", "aria-label": label,
      "data-fid": "p-" + m.n + "-" + key, value: cur[key], disabled, oninput: edit(key) });
    const pickAdv = (side) => () => {
      const dr = state.drafts[m.n] && state.drafts[m.n].dirty ? state.drafts[m.n] : Object.assign({}, cur);
      dr.adv = side; dr.dirty = true; dr.status = ""; state.drafts[m.n] = dr; scheduleSave(m);
    };
    const advRow = isKO && tie && known
      ? h("div", { class: "adv" }, "Tasapeli, jatkoon:",
        h("label", null, h("input", { type: "radio", name: "adv" + m.n, checked: cur.adv === "home", disabled, onchange: pickAdv("home") }), homeName),
        h("label", null, h("input", { type: "radio", name: "adv" + m.n, checked: cur.adv === "away", disabled, onchange: pickAdv("away") }), awayName))
      : null;

    const info = [];
    if (m.result) {
      const sameTeams = !isKO || (m.actualHome && m.home && m.actualHome.code === m.home.code && m.actualAway.code === m.away.code);
      info.push(h("span", null, "Oikea tulos " + m.result.homeGoals + "–" + m.result.awayGoals + (isKO && m.actualHome ? " (" + teamName(m.actualHome) + " – " + teamName(m.actualAway) + ")" : "")));
      info.push(m.points != null ? h("span", { class: "pts" }, "+" + m.points + " p") : h("span", null, sameTeams ? "" : "Eri pari kuin veikkasit: ei ottelupisteitä"));
    } else if (!known && !d.locked) {
      info.push(h("span", null, isKO ? "Täytä ensin edeltävät ottelut, niin joukkueet ilmestyvät tähän." : ""));
    }
    if (cur.status) info.push(h("span", { class: cur.status === "Tallennettu" ? "okmsg" : "err" }, cur.status));

    const others = h("div", { class: "note" });
    const showOthers = h("button", { class: "btn small", type: "button" }, "Kaikkien veikkaukset");
    showOthers.addEventListener("click", async () => {
      clearNode(others);
      try {
        const r = await api("GET", "/matches/" + m.n + "/predictions");
        const rows = r.predictions.slice();
        if (m.result) rows.sort((x, y) => (y.points || 0) - (x.points || 0));
        others.append(...(rows.length ? rows.map((x) => h("div", null, x.nickname + ": " + x.homeGoals + "–" + x.awayGoals + (x.points != null ? " (+" + x.points + " p)" : ""))) : ["Kukaan ei veikannut tätä."]));
        if (r.total > rows.length) others.append(h("div", null, "…ja " + (r.total - rows.length) + " muuta"));
      } catch (e) { others.append(e.message); }
    });

    return h("div", { class: "match", cssVars: m.group ? { "--gc": GC[m.group] } : null },
      h("div", { class: "meta" }, h("span", null, kickoffText(m)), h("span", null, m.venue), h("span", null, "Ottelu " + m.n)),
      h("div", { class: "fixture" },
        h("div", { class: "tm home" + (m.home ? "" : " ph") }, homeName),
        h("div", { class: "sc" }, mk("h", "Kotijoukkueen maalit, ottelu " + m.n), h("span", null, "–"), mk("a", "Vierasjoukkueen maalit, ottelu " + m.n)),
        h("div", { class: "tm" + (m.away ? "" : " ph") }, awayName)),
      advRow,
      info.length ? h("div", { class: "status" }, info) : null,
      d.locked && !isKO && !d.readonly ? h("div", { class: "row" }, showOthers) : null,
      others);
  }

  /* ---------- kaaviopuu, toisen pelaajan veikkaukset, jakaminen ---------- */
  function viewTree(d) {
    const cols = [["Kahdeksannesvälierät", 37, 44], ["Neljännesvälierät", 45, 48], ["Välierät", 49, 50], ["Finaali", 51, 51]];
    const box = (m) => {
      const p = m.prediction;
      const homeWins = p && (p.homeGoals > p.awayGoals || (p.homeGoals === p.awayGoals && p.advance === "home"));
      const awayWins = p && (p.awayGoals > p.homeGoals || (p.homeGoals === p.awayGoals && p.advance === "away"));
      const row = (team, slot, goals, win) => h("div", { class: "trow" + (win ? " win" : "") },
        h("span", { class: team ? "" : "ph" }, team ? teamName(team) : slotLabel(slot)), h("b", null, goals == null ? "" : goals));
      let real = null;
      if (m.result && m.actualHome && m.actualAway) {
        real = h("div", { class: "tmeta" }, "Oikea: " + teamName(m.actualHome) + " " + m.result.homeGoals + "–" + m.result.awayGoals + " " + teamName(m.actualAway));
      }
      return h("div", { class: "tbox" },
        row(m.home, m.homeSlot, p ? p.homeGoals : null, homeWins), row(m.away, m.awaySlot, p ? p.awayGoals : null, awayWins),
        h("div", { class: "tmeta" }, "Ottelu " + m.n + (m.points != null ? ", +" + m.points + " p" : "")), real);
    };
    return [
      h("p", { class: "note" }, "Pudotuspelikaavio veikkauksiesi mukaan. Voittaja on lihavoitu, ja lukituksen jälkeen näet oikeat tulokset."),
      h("div", { class: "tree scroll" }, cols.map(([name, a, b]) => h("div", { class: "tcol" },
        h("div", { class: "tname" }, name),
        h("div", { class: "tlist" }, d.matches.filter((m) => m.n >= a && m.n <= b).map(box))))),
    ];
  }

  async function openPlayer(nick) {
    try {
      const data = await api("GET", "/players/" + encodeURIComponent(nick));
      state.player = { nick: data.player || nick, data, sub: "groups" };
      render();
      window.scrollTo(0, 0);
    } catch (e) {
      if (e.status === 401) toLogin(); else window.alert(e.message);
    }
  }

  function viewPlayerPage() {
    const p = state.player, d = p.data, sc = d.score;
    const subs = [["groups", "Lohkot"], ["ko", "Pudotuspelit"]];
    const seg = h("div", { class: "seg", role: "tablist" }, subs.map(([id, label]) => h("button", {
      type: "button", role: "tab", "aria-selected": String(p.sub === id), onclick: () => { p.sub = id; render(); },
    }, label)));
    const body = p.sub === "groups" ? viewGroups(d) : viewKnockout(d);
    return [
      h("button", { class: "btn small", type: "button", onclick: () => { state.player = null; render(); } }, "← Takaisin"),
      h("h2", { class: "ph2" }, p.nick),
      h("div", { class: "banner locked" }, h("b", null, sc.total + " pistettä"),
        " (ottelut " + (sc.total - sc.advancement - sc.scorer) + ", jatko ja mestari " + sc.advancement + ", maalikuningas " + sc.scorer + "). Maalikuningasveikkaus: " + (d.scorerPick || "ei valittu") + "."),
      seg, body,
    ];
  }

  async function shareBracket() {
    const d = state.data;
    const text = "Veikkaukseni EM 2028 -kisoihin" + (d.champion ? ": mestari " + teamName(d.champion) : "") +
      (state.scorer && state.scorer.player ? ", maalikuningas " + state.scorer.player : "") + ". Veikkaa sinäkin: " + location.origin;
    if (navigator.share) { try { await navigator.share({ text }); return "Jaettu"; } catch { return ""; } }
    try { await navigator.clipboard.writeText(text); return "Kopioitu leikepöydälle"; }
    catch { return "Kopioi itse: " + text; }
  }

  /* ---------- maalikuningas ---------- */
  const SUGGEST = ["Harry Kane", "Kylian Mbappé", "Erling Haaland", "Lamine Yamal", "Robert Lewandowski", "Florian Wirtz", "Jamal Musiala", "Viktor Gyökeres", "Romelu Lukaku", "Jude Bellingham"];
  function viewScorer() {
    const s = state.scorer || {};
    const locked = !!s.locked;
    const name = h("input", { type: "text", id: "sname", maxlength: 40, autocomplete: "off", value: s.player || "", disabled: locked, list: "plist" });
    const list = h("datalist", { id: "plist" }, SUGGEST.map((p) => h("option", { value: p })));
    const goals = h("input", { type: "text", id: "sgoals", inputmode: "numeric", maxlength: 2, value: s.goals != null ? String(s.goals) : "", disabled: locked });
    goals.addEventListener("input", () => { goals.value = goals.value.replace(/\D/g, "").slice(0, 2); });
    const msg = h("div", { role: "status" });
    const save = h("button", { class: "btn primary", type: "button", disabled: locked }, "Tallenna");
    save.addEventListener("click", async () => {
      msg.className = ""; msg.textContent = ""; save.disabled = true;
      try {
        const r = await api("PUT", "/scorer", { player: name.value, goals: goals.value === "" ? null : +goals.value });
        state.scorer = Object.assign({}, state.scorer, { player: r.player, goals: goals.value === "" ? undefined : +goals.value });
        msg.className = "okmsg"; msg.textContent = "Tallennettu";
      } catch (e) { msg.className = "err"; msg.textContent = e.message; }
      save.disabled = locked;
    });
    const picks = h("div", { class: "chips" }, SUGGEST.map((p) => h("button", { class: "chip", type: "button", disabled: locked,
      onclick: () => { name.value = p; } }, p)));
    const dd = state.data;
    const allDone = dd && dd.predicted === dd.total && !!s.player && !locked;
    return [
      allDone ? h("div", { class: "card done-card" }, h("b", null, "Kaikki veikkaukset on tehty ✓"),
        h("p", { class: "note" }, "Voit muuttaa niitä milloin vain, kunnes turnaus alkaa. Pisteet näet Tulostaulusta."),
        h("button", { class: "btn small", type: "button", onclick: async (e) => { e.target.textContent = (await shareBracket()) || "Jaa veikkaukseni"; } }, "Jaa veikkaukseni")) : null,
      h("p", { class: "lead" }, locked ? "Veikkausaika on päättynyt." : "Kuka tekee turnauksen eniten maaleja? Voit vaihtaa valintaasi, kunnes turnaus alkaa."),
      h("div", { class: "card" },
        field("sname", "Pelaajan nimi", name), list,
        field("sgoals", "Montako maalia hän tekee (valinnainen)", goals), msg, h("div", { class: "row" }, save)),
      h("p", { class: "note" }, "Ehdotuksia:"), picks,
      s.winners ? h("div", { class: "card" }, h("p", { class: "note" }, "Maalikuningas"), h("div", { class: "big" }, s.winners.join(", ")),
        h("p", null, s.points ? "Sait " + s.points + " pistettä." : "Ei pisteitä tällä kertaa.")) : null,
    ];
  }

  /* ---------- tulostaulu ---------- */
  function viewBoard() {
    const holder = h("div", null, h("p", { class: "note" }, "Ladataan…"));
    loadBoard(holder);
    state.timer = setInterval(() => { if (holder.isConnected && !document.hidden) loadBoard(holder); }, 30000);
    const sc = state.data && state.data.score;
    const matchPts = sc ? sc.total - sc.advancement - sc.scorer : 0;
    return [
      h("h2", null, "Tulostaulu"),
      h("p", { class: "lead" }, "Kaikkien pelaajien pisteet. Taulukko päivittyy, kun ylläpito on syöttänyt ottelun tuloksen."),
      sc ? h("div", { class: "card" },
        h("div", { class: "breakdown" },
          h("div", null, h("span", { class: "note" }, "Pisteesi"), h("b", null, sc.total)),
          h("div", null, h("span", { class: "note" }, "Ottelutulokset"), h("b", null, matchPts)),
          h("div", null, h("span", { class: "note" }, "Kaavio ja mestari"), h("b", null, sc.advancement)),
          h("div", null, h("span", { class: "note" }, "Maalikuningas"), h("b", null, sc.scorer)))) : null,
      holder,
    ];
  }

  async function loadBoard(holder) {
    try {
      const d = await api("GET", "/leaderboard");
      const row = (r) => h("tr", { class: (r.you ? "you " : "") + (r.rank <= 3 && r.points > 0 ? "r" + r.rank : "") },
        h("td", { class: "rank" }, r.rank, deltaMark(r.delta)),
        h("td", { class: "l" }, state.data && state.data.locked
          ? h("button", { class: "linkbtn", type: "button", onclick: () => openPlayer(r.nickname) }, r.nickname) : r.nickname),
        h("td", { class: "pts-cell" }, r.points));
      const rows = d.entries.map(row);
      if (d.you && !d.entries.some((e) => e.you)) rows.push(h("tr", { class: "gap" }, h("td", { colspan: "3" }, "…")), row(d.you));
      clearNode(holder);
      if (!d.entries.length) { holder.append(h("p", { class: "note" }, "Kukaan ei ole vielä veikannut.")); return; }
      holder.append(h("div", { class: "card" },
        h("p", { class: "note" }, d.total + (d.total === 1 ? " pelaaja" : " pelaajaa") + (d.total > 100 ? ", näytetään 100 parasta ja sinut" : "")),
        h("div", { class: "scroll" }, h("table", null,
          h("thead", null, h("tr", null, h("th", null, "#"), h("th", { class: "l" }, "Pelaaja"), h("th", null, "Pisteet"))),
          h("tbody", null, rows)))));
    } catch (e) {
      if (e.status === 401) { toLogin(); return; }
      clearNode(holder); holder.append(h("p", { class: "err" }, e.message));
    }
  }

  /* ---------- chat ---------- */
  function viewChat() {
    const log = h("div", { class: "chatlog", role: "log", "aria-live": "polite", "aria-label": "Chat" });
    const verified = !state.me || state.me.emailVerified;
    const input = h("input", { type: "text", id: "chatmsg", maxlength: 300, autocomplete: "off", placeholder: verified ? "Kirjoita viesti…" : "Vahvista sähköposti kirjoittaaksesi", "aria-label": "Viesti", disabled: !verified });
    const err = h("div", { class: "err", role: "alert" });
    const send = h("button", { class: "btn primary", type: "button", disabled: !verified }, "Lähetä");
    let lastId = 0;
    const add = (msgs) => {
      const fresh = msgs.filter((m) => m.id > lastId);
      if (!fresh.length) return;
      const nearBottom = log.scrollHeight - log.scrollTop - log.clientHeight < 80;
      for (const m of fresh) {
        lastId = m.id;
        const rep = m.mine ? null : h("button", { class: "linkbtn rep", type: "button", "aria-label": "Ilmianna viesti" }, "Ilmianna");
        if (rep) {
          rep.addEventListener("click", async () => {
            rep.disabled = true;
            try { await api("POST", "/chat/" + m.id + "/report", {}); rep.textContent = "Ilmiannettu"; }
            catch (e) { rep.textContent = e.message; }
          });
        }
        log.append(h("div", { class: "msg" + (m.mine ? " mine" : "") },
          h("span", { class: "who" }, m.nickname), h("span", { class: "when" }, fmtClock.format(new Date(m.at * 1000))), rep,
          h("div", { class: "txt" }, m.body)));
      }
      if (nearBottom) log.scrollTop = log.scrollHeight;
    };
    const poll = async () => {
      if (!log.isConnected || document.hidden) return;
      try { add(await api("GET", "/chat?after=" + lastId)); }
      catch (e) { if (e.status === 401) toLogin(); }
    };
    const submit = async () => {
      if (!input.value.trim()) return;
      err.textContent = ""; send.disabled = true;
      try { await api("POST", "/chat", { body: input.value }); input.value = ""; await poll(); }
      catch (e) { err.textContent = e.message; }
      send.disabled = !verified; input.focus();
    };
    send.addEventListener("click", submit);
    input.addEventListener("keydown", (e) => { if (e.key === "Enter") { e.preventDefault(); submit(); } });
    state.timer = setInterval(poll, 4000);
    queueMicrotask(poll);
    return [
      h("h2", null, "Chat"),
      h("p", { class: "lead" }, "Viestit näkyvät kaikille pelaajille. Älä kirjoita henkilötietojasi."),
      log, h("div", { class: "row chatform" }, input, send), err,
    ];
  }

  /* ---------- tili ---------- */
  function viewAccount() {
    const cur = passwordInput("curpw", "current-password");
    const next = passwordInput("newpw", "new-password");
    const pwMsg = h("div", { role: "status" });
    const pwBtn = h("button", { class: "btn primary", type: "button" }, "Vaihda salasana");
    pwBtn.addEventListener("click", async () => {
      pwMsg.className = ""; pwMsg.textContent = ""; pwBtn.disabled = true;
      try {
        await api("POST", "/me/password", { current: cur.input.value, new: next.input.value });
        cur.input.value = ""; next.input.value = "";
        pwMsg.className = "okmsg"; pwMsg.textContent = "Salasana vaihdettu. Muut laitteet kirjattiin ulos.";
      } catch (e) { pwMsg.className = "err"; pwMsg.textContent = e.message; }
      pwBtn.disabled = false;
    });
    const logout = h("button", { class: "btn", type: "button" }, "Kirjaudu ulos");
    logout.addEventListener("click", async () => { try { await api("POST", "/logout"); } catch { /* ei väliä */ } toLogin(); });

    const delPw = passwordInput("delpw", "current-password");
    const delMsg = h("div", { class: "err", role: "alert" });
    const del = h("button", { class: "btn danger", type: "button" }, "Poista tunnukseni ja kaikki tietoni");
    del.addEventListener("click", async () => {
      if (!del.classList.contains("sure")) { del.classList.add("sure"); del.textContent = "Vahvista: poistetaan pysyvästi"; return; }
      delMsg.textContent = "";
      try { await api("DELETE", "/me", { password: delPw.input.value }); toLogin(); }
      catch (e) { delMsg.textContent = e.message; del.classList.remove("sure"); del.textContent = "Poista tunnukseni ja kaikki tietoni"; }
    });

    const remMsg = h("span", { class: "note", role: "status" });
    const rem = h("input", { type: "checkbox", id: "rem", checked: !!(state.me && state.me.reminders) });
    rem.addEventListener("change", async () => {
      try { await api("POST", "/me/reminders", { enabled: rem.checked }); if (state.me) state.me.reminders = rem.checked; remMsg.textContent = " Tallennettu"; }
      catch (e) { rem.checked = !rem.checked; remMsg.textContent = " " + e.message; }
    });
    const expMsg = h("span", { class: "err", role: "alert" });
    const exp = h("button", { class: "btn", type: "button" }, "Lataa tietoni");
    exp.addEventListener("click", async () => {
      expMsg.textContent = "";
      try {
        const res = await fetch("/api/me/export", { credentials: "same-origin", headers: { "X-Requested-With": "em2028" } });
        if (!res.ok) throw new Error("Tietojen vienti epäonnistui");
        const url = URL.createObjectURL(await res.blob());
        const a = h("a", { href: url, download: "em2028-tietoni.json" });
        document.body.append(a); a.click(); a.remove();
        setTimeout(() => URL.revokeObjectURL(url), 2000);
      } catch (e) { expMsg.textContent = e.message; }
    });
    const remCard = h("div", { class: "card" },
      h("h3", null, "Muistutukset ja tiedot"),
      h("div", { class: "row" }, h("label", null, rem, " Lähetä sähköpostimuistutus ennen veikkausten sulkeutumista"), remMsg),
      h("p", { class: "note" }, "Saat muistutuksen vain, jos veikkauksesi ovat kesken. Jokaisessa viestissä on myös peruutuslinkki."),
      h("div", { class: "row" }, exp, expMsg),
      h("p", { class: "note" }, "Lataa kaikki sinusta tallennetut tiedot JSON-tiedostona."));
    return [
      h("h2", null, "Tili"),
      h("div", { class: "card" },
        h("p", null, "Kirjautuneena: ", h("strong", null, state.me ? state.me.nickname : "")),
        h("p", { class: "note" }, state.me && state.me.emailVerified ? "Sähköposti vahvistettu." : "Sähköpostia ei ole vielä vahvistettu."),
        h("div", { class: "row" }, logout)),
      remCard,
      h("div", { class: "card" },
        h("h3", null, "Näin pisteet lasketaan"),
        h("ul", { class: "rules" },
          h("li", null, "Täsmälleen oikea tulos: 3 pistettä."),
          h("li", null, "Oikea voittaja tai tasapeli, mutta väärä tulos: 1 piste."),
          h("li", null, "Oikein veikattu joukkue pudotuspelien kierrokselle: 2–5 pistettä, sitä enemmän mitä pidemmälle."),
          h("li", null, "Oikea mestari: 10 pistettä. Oikea maalikuningas: 10 pistettä.")),
        h("p", { class: "note" }, "Tulos tarkoittaa varsinaisen peliajan (90 min) tulosta. Kaikki veikkaukset lukittuvat, kun turnauksen ensimmäinen ottelu alkaa.")),
      h("div", { class: "card" },
        h("h3", null, "Vaihda salasana"),
        field("curpw", "Nykyinen salasana", cur.node), field("newpw", "Uusi salasana", next.node, "Vähintään 10 merkkiä."),
        pwMsg, h("div", { class: "row" }, pwBtn)),
      h("div", { class: "card" },
        h("h3", null, "Tietosuoja"),
        h("p", { class: "note" }, "Tallennamme nimimerkkisi, salatun sähköpostiosoitteesi, salasanasi tiivisteen, veikkauksesi ja chat-viestisi. Nimimerkkisi, pisteesi ja chat-viestisi näkyvät kaikille pelaajille. Emme tallenna IP-osoitettasi. Lue lisää ", h("a", { href: "privacy.html" }, "tietosuojaselosteesta"), "."),
        field("delpw", "Salasana vahvistukseksi", delPw.node), delMsg, del),
    ];
  }

  /* ---------- käynnistys ---------- */
  (async () => {
    try { state.me = await api("GET", "/me"); await start(); }
    catch (e) {
      if (e.status === 401) renderAuth();
      else { clearNode(app); app.append(h("p", { class: "err" }, e.message)); }
    }
  })();
})();
