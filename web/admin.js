"use strict";
(() => {
  // Näkymät rakennetaan DOM-solmuina (textContent), joten käyttäjien syöttämä sisältö ei voi suorittaa koodia.
  // Todellinen pääsynhallinta on palvelimella: tämä sivu vain piilottaa toiminnot, joihin ei ole oikeutta.
  const app = document.getElementById("app");
  const tabsEl = document.getElementById("tabs");
  const toastEl = document.getElementById("toast");
  const state = { perms: new Set(), tab: "overview", teams: [], mfilter: "open", msub: "groups", agroup: "A", aday: "", timer: null };

  /* ---------- apurit ---------- */
  function h(tag, props, ...kids) {
    const el = document.createElement(tag);
    for (const [k, v] of Object.entries(props || {})) {
      if (v === false || v == null) continue;
      if (k === "class") el.className = v;
      else if (k === "value") el.value = v;
      else if (k === "selected") el.selected = !!v;
      else if (k === "checked") el.checked = !!v;
      else if (k === "disabled") el.disabled = !!v;
      else if (k === "hidden") el.hidden = !!v;
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
  const fmtDT = new Intl.DateTimeFormat("fi-FI", { dateStyle: "short", timeStyle: "short" });
  const tz = Intl.DateTimeFormat().resolvedOptions().timeZone;
  const pad = (n) => String(n).padStart(2, "0");
  const toLocal = (unix) => {
    const d = new Date(unix * 1000);
    return d.getFullYear() + "-" + pad(d.getMonth() + 1) + "-" + pad(d.getDate()) + "T" + pad(d.getHours()) + ":" + pad(d.getMinutes());
  };
  const fromLocal = (v) => { const t = new Date(v).getTime(); return v && !isNaN(t) ? Math.floor(t / 1000) : 0; };

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
    if (res.status !== 204) { try { data = await res.json(); } catch { /* tyhjä */ } }
    if (!res.ok) throw Object.assign(new Error((data && data.error) || "Jokin meni pieleen"), { status: res.status, code: data && data.code });
    return data;
  }

  let toastTimer;
  function toast(msg, ok = true) {
    toastEl.textContent = msg;
    toastEl.className = "toast show " + (ok ? "okmsg" : "err");
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => { toastEl.className = "toast"; toastEl.textContent = ""; }, 4500);
  }
  function handleErr(e) {
    if (e.status === 401) { init(); return; }
    toast(e.message, false);
  }
  function stopTimer() { if (state.timer) { clearInterval(state.timer); state.timer = null; } }

  // Kaksivaiheinen vahvistus vaarallisille toiminnoille.
  function confirmButton(label, sureLabel, cls, fn) {
    const b = h("button", { class: "btn small " + cls, type: "button" }, label);
    let t;
    b.addEventListener("click", async () => {
      if (!b.classList.contains("sure")) {
        b.classList.add("sure"); b.textContent = sureLabel;
        t = setTimeout(() => { b.classList.remove("sure"); b.textContent = label; }, 4000);
        return;
      }
      clearTimeout(t);
      b.disabled = true;
      try { await fn(); } catch (e) { handleErr(e); }
      b.disabled = false; b.classList.remove("sure"); b.textContent = label;
    });
    return b;
  }

  /* ---------- kirjautuminen ja oikeudet ---------- */
  // Salasanakenttä, jossa on Näytä/Piilota-painike. Painike ei vie kentältä kohdistusta (näppäimistö pysyy auki).
  function passwordField(id, autocomplete, extra) {
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

  function renderLogin(note) {
    stopTimer();
    tabsEl.hidden = true;
    clearNode(app);
    const email = h("input", { type: "email", id: "email", autocomplete: "username", maxlength: 254 });
    const pw = passwordField("pw", "current-password");
    const err = h("div", { class: "err", role: "alert" }, note || "");
    const btn = h("button", { class: "btn primary", type: "button" }, "Kirjaudu");
    const go = async () => {
      err.textContent = ""; btn.disabled = true;
      try { await api("POST", "/login", { email: email.value, password: pw.input.value }); pw.input.value = ""; await init(); }
      catch (e) { err.textContent = e.message; }
      btn.disabled = false;
    };
    btn.addEventListener("click", go);
    for (const i of [email, pw.input]) i.addEventListener("keydown", (e) => { if (e.key === "Enter") go(); });
    app.append(h("section", { class: "view" },
      h("h2", null, "Kirjaudu ylläpitoon"),
      h("p", { class: "lead" }, "Käytä pelaajatunnuksesi sähköpostia ja salasanaa. Ylläpito vaatii tuoreen kirjautumisen: istunto vanhenee 12 tunnissa."),
      h("div", { class: "card accent" },
        h("div", { class: "field" }, h("label", { for: "email" }, "Sähköposti"), email),
        h("div", { class: "field" }, h("label", { for: "pw" }, "Salasana"), pw.node), err, btn)));
    email.focus();
  }

  async function renderNoRights() {
    stopTimer();
    tabsEl.hidden = true;
    clearNode(app);
    let nick = "";
    try { nick = (await api("GET", "/me")).nickname; } catch { /* ei väliä */ }
    app.append(h("section", { class: "view" },
      h("h2", null, "Ei ylläpito-oikeuksia"),
      h("p", { class: "lead" }, "Tilillä ei ole ylläpito-oikeuksia. Oikeudet myöntää palvelimen ylläpitäjä komentorivillä:"),
      h("div", { class: "keybox" }, "em2028 admin grant " + (nick || "<nimimerkki>") + " all"),
      h("p", { class: "note" }, "Oikeudet: results (tulokset), setup (joukkueet ja ajat), moderate (käyttäjät ja chat), audit (loki). Pilkuilla erotettuna tai all.")));
  }

  // Kaksivaiheinen kirjautuminen: koodi todentajasovelluksesta (TOTP). Vahvistus kestää 12 tuntia.
  function renderMFA() {
    stopTimer();
    tabsEl.hidden = true;
    clearNode(app);
    const code = h("input", { type: "text", id: "mfa", inputmode: "numeric", autocomplete: "one-time-code", maxlength: 7 });
    const err = h("div", { class: "err", role: "alert" });
    const btn = h("button", { class: "btn primary", type: "button" }, "Vahvista");
    const go = async () => {
      err.textContent = ""; btn.disabled = true;
      try { await api("POST", "/admin/mfa", { code: code.value }); await init(); }
      catch (e) { err.textContent = e.message; btn.disabled = false; }
    };
    btn.addEventListener("click", go);
    code.addEventListener("keydown", (e) => { if (e.key === "Enter") go(); });
    app.append(h("section", { class: "view" },
      h("h2", null, "Kaksivaiheinen kirjautuminen"),
      h("p", { class: "lead" }, "Anna 6-numeroinen koodi todentajasovelluksestasi. Jos puhelin on hukassa, palvelimen ylläpitäjä voi nollata asetuksen komennolla: em2028 admin totp-reset <nimimerkki>."),
      h("div", { class: "card accent" }, h("div", { class: "field" }, h("label", { for: "mfa" }, "Koodi"), code), err, btn)));
    code.focus();
  }

  async function renderMFASetup(required) {
    stopTimer();
    tabsEl.hidden = true;
    clearNode(app);
    const box = h("div", { class: "card" });
    app.append(h("section", { class: "view" },
      h("h2", null, "Ota kaksivaiheinen kirjautuminen käyttöön"),
      h("p", { class: "lead" }, (required ? "Ylläpito vaatii kaksivaiheisen kirjautumisen. " : "") +
        "Tarvitset todentajasovelluksen (esim. Google Authenticator, Authy, 1Password tai Bitwarden)."), box));
    const start = h("button", { class: "btn primary", type: "button" }, "Aloita");
    box.append(start);
    start.addEventListener("click", async () => {
      start.disabled = true;
      try {
        const r = await api("POST", "/admin/totp/setup", {});
        clearNode(box);
        const code = h("input", { type: "text", id: "mfa", inputmode: "numeric", autocomplete: "one-time-code", maxlength: 7 });
        const err = h("div", { class: "err", role: "alert" });
        const ok = h("button", { class: "btn primary", type: "button" }, "Vahvista ja ota käyttöön");
        ok.addEventListener("click", async () => {
          err.textContent = ""; ok.disabled = true;
          try { await api("POST", "/admin/totp/confirm", { code: code.value }); await init(); }
          catch (e) { err.textContent = e.message; ok.disabled = false; }
        });
        box.append(
          h("p", null, "1. Lisää uusi tili todentajasovellukseen ja valitse \"syötä asetusavain käsin\" (aikaperusteinen):"),
          h("div", { class: "keybox" }, r.secret),
          h("p", { class: "note" }, "Tai kopioi osoite sellaisenaan, jos sovellus tukee sitä: " + r.uri),
          h("p", null, "2. Kirjoita sovelluksen näyttämä 6-numeroinen koodi:"),
          h("div", { class: "field" }, h("label", { for: "mfa" }, "Koodi"), code), err, ok,
          h("p", { class: "note" }, "Säilytä asetusavain turvallisessa paikassa. Jos puhelin katoaa, palvelimen ylläpitäjä voi nollata asetuksen."));
        code.focus();
      } catch (e) { start.disabled = false; box.append(h("p", { class: "err" }, e.message)); }
    });
  }

  async function init() {
    stopTimer();
    try {
      const me = await api("GET", "/admin/me");
      state.perms = new Set(me.perms);
      state.mfa = me.mfa || {};
      if (state.mfa.enabled && !state.mfa.verified) return renderMFA();
      if (state.mfa.required && !state.mfa.enabled) return renderMFASetup(true);
      state.teams = await api("GET", "/admin/teams");
      document.getElementById("sub").textContent = "Kirjautuneena " + me.nickname + ". Oikeudet: " + me.perms.join(", ");
      if (!tabAllowed(state.tab)) state.tab = "overview";
      render();
    } catch (e) {
      if (e.status === 401 && e.code === "mfa") return renderMFA();
      if (e.status === 403 && e.code === "mfa_setup") return renderMFASetup(true);
      if (e.status === 401) return renderLogin(e.code === "reauth" ? e.message : "");
      if (e.status === 403) return renderNoRights();
      clearNode(app); tabsEl.hidden = true;
      app.append(h("p", { class: "err" }, e.message));
    }
  }

  /* ---------- runko ---------- */
  const TABS = [
    ["overview", "Yleiskatsaus", null, viewOverview],
    ["matches", "Ottelut", null, viewMatches],
    ["teams", "Joukkueet", null, viewTeams],
    ["bracket", "Kaavio", null, viewBracket],
    ["scorer", "Maalikuningas", "results", viewScorer],
    ["users", "Käyttäjät", "moderate", viewUsers],
    ["chat", "Chat", "moderate", viewChat],
    ["admins", "Ylläpitäjät ja loki", null, viewAdmins],
  ];
  const tabAllowed = (id) => TABS.some((t) => t[0] === id && (!t[2] || state.perms.has(t[2])));

  function render() {
    stopTimer();
    tabsEl.hidden = false;
    clearNode(tabsEl);
    for (const [id, label, perm] of TABS) {
      if (perm && !state.perms.has(perm)) continue;
      tabsEl.append(h("button", {
        role: "tab", type: "button", "aria-selected": String(state.tab === id),
        onclick: () => { state.tab = id; render(); window.scrollTo(0, 0); },
      }, label));
    }
    clearNode(app);
    const box = h("section", { class: "view" });
    app.append(box);
    const t = TABS.find((x) => x[0] === state.tab);
    Promise.resolve(t[3](box)).catch(handleErr);
  }

  /* ---------- yleiskatsaus ---------- */
  async function viewOverview(box) {
    const d = await api("GET", "/admin/overview");
    const stat = (label, v) => h("div", { class: "stat" }, h("span", { class: "big" }, v), h("span", { class: "note" }, label));
    box.append(h("h2", null, "Yleiskatsaus"),
      h("div", { class: "stats" },
        stat("Pelaajia", d.users), stat("Veikannut", d.predictors), stat("Veikkauksia", d.predictions),
        stat("Ottelua pelattu", d.finished + " / 51"), stat("Chat-viestejä", d.chat), stat("Mykistettyjä", d.muted)),
      h("p", { class: "note" }, "Käytössä olevat oikeutesi: " + [...state.perms].join(", ") + ". Kaikki muutokset kirjataan ylläpitolokiin."));
  }

  /* ---------- ottelut ---------- */
  function slotLabel(s) {
    if (!s) return "Selviää myöhemmin";
    if (/^[12][A-F]$/.test(s)) return s[0] === "1" ? "Lohkon " + s[1] + " voittaja" : "Lohkon " + s[1] + " kakkonen";
    if (s.startsWith("T:")) return "Kolmonen (" + s.slice(2).split("").join("/") + ")";
    if (s[0] === "W") return "Ottelun " + s.slice(1) + " voittaja";
    return s;
  }

  /* Ottelut: virallisten tulosten syöttö. Alanäkymät: lohkot (taulukko + tulokset), päivät, pudotuspelit ja ajat. */
  const GC = { A: "var(--A)", B: "var(--B)", C: "var(--C)", D: "var(--D)", E: "var(--E)", F: "var(--F)" };
  const teamName = (t) => (t ? t.name || t.code : "");
  const codeName = (code) => { const t = state.teams.find((x) => x.code === code); return t && t.name !== t.code ? t.name : code; };
  const fmtMatchTime = new Intl.DateTimeFormat("fi-FI", { weekday: "short", day: "numeric", month: "numeric", hour: "2-digit", minute: "2-digit" });
  const fmtMatchDay = new Intl.DateTimeFormat("fi-FI", { weekday: "short", day: "numeric", month: "numeric", timeZone: "UTC" });
  const dayKey = (m) => {
    const d = new Date(m.kickoff * 1000);
    return m.kickoffConfirmed ? d.getFullYear() + "-" + pad(d.getMonth() + 1) + "-" + pad(d.getDate())
      : d.getUTCFullYear() + "-" + pad(d.getUTCMonth() + 1) + "-" + pad(d.getUTCDate());
  };

  // Yksi ottelu tiiviinä tulosriviksi. collect() palauttaa tallennettavan muutoksen tai null, jos ei muutosta.
  function resultRow(m, reloadAll) {
    const canRes = state.perms.has("results");
    const isKO = m.stage !== "group";
    const known = !!(m.home && m.away);
    const disabled = !canRes || !known;
    const goal = (v, label) => h("input", { type: "text", inputmode: "numeric", maxlength: 2, autocomplete: "off", value: v, disabled, "aria-label": label });
    const hg = goal(m.result ? String(m.result.homeGoals) : "", "Kotimaalit, ottelu " + m.n);
    const ag = goal(m.result ? String(m.result.awayGoals) : "", "Vierasmaalit, ottelu " + m.n);
    let adv = m.result && m.result.advance ? m.result.advance : "";
    const tie = () => hg.value !== "" && ag.value !== "" && +hg.value === +ag.value;
    const home = m.home ? teamName(m.home) : slotLabel(m.homeSlot);
    const away = m.away ? teamName(m.away) : slotLabel(m.awaySlot);
    const rHome = h("input", { type: "radio", name: "radv" + m.n, value: "home", checked: adv === "home", disabled });
    const rAway = h("input", { type: "radio", name: "radv" + m.n, value: "away", checked: adv === "away", disabled });
    for (const r of [rHome, rAway]) r.addEventListener("change", () => { adv = r.value; });
    const advRow = h("div", { class: "adv", hidden: true }, "Tasapeli, jatkoon:", h("label", null, rHome, home), h("label", null, rAway, away));
    const sync = () => { advRow.hidden = !(isKO && tie()); };
    for (const i of [hg, ag]) i.addEventListener("input", () => { i.value = i.value.replace(/\D/g, "").slice(0, 2); sync(); });
    sync();

    const status = [];
    if (m.result) status.push(h("span", { class: "pts" }, "Tallennettu " + m.result.homeGoals + "–" + m.result.awayGoals));
    else if (!known) status.push(h("span", null, "Joukkueet puuttuvat: aseta ne Pudotuspelit ja ajat -näkymässä."));
    else if (Date.now() / 1000 < m.kickoff) status.push(h("span", null, "Ottelu ei ole vielä alkanut: tuloksen voi syöttää vasta alkamisajan jälkeen."));
    if (m.result && canRes) {
      status.push(confirmButton("Poista tulos", "Vahvista", "danger", async () => {
        await api("PUT", "/admin/matches/" + m.n, { clearResult: true }); toast("Ottelun " + m.n + " tulos poistettu"); await reloadAll();
      }));
    }
    const node = h("div", { class: "match", cssVars: m.group ? { "--gc": GC[m.group] } : null },
      h("div", { class: "meta" },
        h("span", null, m.kickoffConfirmed ? fmtMatchTime.format(new Date(m.kickoff * 1000)) : fmtMatchDay.format(new Date(m.kickoff * 1000)) + ", aika vahvistamatta"),
        h("span", null, m.venue), h("span", null, "Ottelu " + m.n)),
      h("div", { class: "fixture" },
        h("div", { class: "tm home" + (m.home ? "" : " ph") }, home),
        h("div", { class: "sc" }, hg, h("span", null, "–"), ag),
        h("div", { class: "tm" + (m.away ? "" : " ph") }, away)),
      advRow,
      status.length ? h("div", { class: "status" }, status) : null);

    return {
      node,
      collect() {
        if (disabled || hg.value === "" || ag.value === "") return null;
        const cur = m.result;
        const changed = !cur || cur.homeGoals !== +hg.value || cur.awayGoals !== +ag.value || (isKO && tie() && cur.advance !== adv);
        if (!changed) return null;
        if (isKO && tie() && !adv) return { n: m.n, error: "valitse jatkoon selviävä joukkue" };
        const body = { homeGoals: +hg.value, awayGoals: +ag.value };
        if (isKO && tie()) body.advance = adv;
        return { n: m.n, body };
      },
    };
  }

  async function saveRows(rows, btn, reloadAll) {
    const todo = rows.map((r) => r.collect()).filter(Boolean);
    if (!todo.length) { toast("Ei tallennettavia muutoksia", false); return; }
    btn.disabled = true;
    let ok = 0;
    const errs = [];
    for (const t of todo) {
      if (t.error) { errs.push("ottelu " + t.n + ": " + t.error); continue; }
      try { await api("PUT", "/admin/matches/" + t.n, t.body); ok++; }
      catch (e) { if (e.status === 401) { init(); return; } errs.push("ottelu " + t.n + ": " + e.message); }
    }
    toast((ok ? "Tallennettu " + ok + " tulosta. " : "") + (errs.length ? "Ei tallennettu: " + errs.slice(0, 2).join("; ") + (errs.length > 2 ? " …" : "") : ""), errs.length === 0);
    await reloadAll();
  }

  const saveButton = (label, rows, reloadAll) => {
    const b = h("button", { class: "btn primary", type: "button", disabled: !state.perms.has("results") }, label);
    b.addEventListener("click", () => saveRows(rows(), b, reloadAll));
    return b;
  };

  async function viewMatches(box) {
    const mx = { matches: [], bracket: null };
    const body = h("div");
    const subs = [["groups", "Lohkot"], ["days", "Päivät"], ["ko", "Pudotuspelit ja ajat"], ["import", "Tuo tulokset"]];
    const reloadAll = async () => {
      [mx.matches, mx.bracket] = await Promise.all([api("GET", "/matches"), api("GET", "/admin/bracket")]);
      draw();
    };
    const draw = () => {
      clearNode(body);
      body.append(h("div", { class: "seg", role: "tablist" }, subs.map(([id, label]) =>
        h("button", { type: "button", role: "tab", "aria-selected": String(state.msub === id), onclick: () => { state.msub = id; draw(); } }, label))));
      if (state.msub === "groups") body.append(groupsView(mx, reloadAll, draw));
      else if (state.msub === "days") body.append(daysView(mx, reloadAll, draw));
      else if (state.msub === "import") body.append(importView(mx, reloadAll));
      else body.append(scheduleView(mx, reloadAll));
    };
    box.append(h("h2", null, "Ottelut"),
      h("p", { class: "lead" }, "Tulos tarkoittaa varsinaisen peliajan (90 min) tulosta. Tulosta ei voi syöttää ennen ottelun alkamisaikaa. Pelaajien pisteet lasketaan näistä virallisista tuloksista."),
      body);
    await reloadAll();
  }

  // Käsin asetettava lohkon sijoitusjärjestys (kun tasatilanne ulottuu kurinpitopisteisiin tai karsintarankingiin).
  function orderEditor(g, grp, mx, reloadAll) {
    if (!state.perms.has("results") || !grp.complete) return null;
    const overridden = (mx.bracket.groupOverrides || []).includes(g);
    const current = grp.order || (grp.table || []).map((t) => t.code);
    const selects = [0, 1, 2, 3].map((i) => h("select", { "aria-label": (i + 1) + ". sija" },
      (grp.table || []).map((t) => h("option", { value: t.code, selected: t.code === current[i] }, codeName(t.code) !== t.code ? codeName(t.code) + " (" + t.code + ")" : t.code))));
    // Valinta, joka on jo toisessa sijassa, vaihtaa paikkaa sen kanssa: järjestys pysyy aina kelvollisena.
    let prev = selects.map((x) => x.value);
    selects.forEach((sel, i) => sel.addEventListener("change", () => {
      const j = selects.findIndex((o, k) => k !== i && o.value === sel.value);
      if (j >= 0) selects[j].value = prev[i];
      prev = selects.map((x) => x.value);
    }));
    const save = h("button", { class: "btn small primary", type: "button" }, "Tallenna järjestys");
    save.addEventListener("click", async () => {
      const order = selects.map((x) => x.value);
      if (new Set(order).size !== 4) { toast("Jokainen joukkue vain kerran", false); return; }
      try { await api("PUT", "/admin/groups/" + g + "/order", { order }); toast("Järjestys tallennettu"); await reloadAll(); } catch (e) { handleErr(e); }
    });
    const clear = overridden ? confirmButton("Palauta automaattinen", "Vahvista", "danger", async () => {
      await api("DELETE", "/admin/groups/" + g + "/order"); toast("Palautettu"); await reloadAll();
    }) : null;
    return h("details", { open: !!grp.unresolved || overridden },
      h("summary", null, grp.unresolved ? "Tasatilanne: aseta järjestys käsin" : overridden ? "Järjestys asetettu käsin" : "Aseta järjestys käsin"),
      h("p", { class: "note" }, "Käytä vain, kun UEFAn kriteerit (kurinpitopisteet, karsintaranking, rangaistuspotkut) ratkaisevat tasatilanteen. Muuten taulukko lasketaan automaattisesti."),
      h("div", { class: "fields-col" }, selects), h("div", { class: "rowbtns" }, save, clear));
  }

  function groupsView(mx, reloadAll, redraw) {
    const g = state.agroup;
    const chips = h("div", { class: "gchips", role: "group", "aria-label": "Valitse lohko" }, Object.keys(GC).map((x) => {
      const ms = mx.matches.filter((m) => m.group === x);
      const done = ms.filter((m) => m.result).length;
      return h("button", { class: "gchip", type: "button", cssVars: { "--gc": GC[x] }, "aria-pressed": String(x === g),
        "aria-label": "Lohko " + x + ", " + done + " / 6 tulosta", onclick: () => { state.agroup = x; redraw(); } }, x, h("small", null, done + "/6"));
    }));

    const grp = mx.bracket.groups[g];
    const rows = (grp.table || []).map((t, i) => h("tr", { class: i < 2 ? "adv-row" : i === 2 ? "third-row" : "" },
      h("td", { class: "pos" }, i + 1),
      h("td", { class: "l" }, codeName(t.code) !== t.code ? [codeName(t.code), " ", h("span", { class: "code" }, t.code)] : t.code),
      h("td", null, t.played), h("td", null, t.won), h("td", null, t.drawn), h("td", null, t.lost),
      h("td", null, t.gf + "–" + t.ga), h("td", { class: "pts-cell" }, t.points)));
    const rowObjs = [];
    const right = h("div");
    [1, 2, 3].forEach((md) => {
      right.append(h("div", { class: "md" }, md + ". kierros"));
      for (const m of mx.matches.filter((x) => x.group === g && Math.ceil(x.n / 12) === md)) {
        const r = resultRow(m, reloadAll); rowObjs.push(r); right.append(r.node);
      }
    });
    const left = h("div", null,
      h("div", { class: "panel", cssVars: { "--gc": GC[g] } },
        h("h3", null, "Lohko " + g + ", virallinen taulukko"),
        grp.unresolved ? h("p", { class: "err" }, "Tasatilanne ulottuu kurinpitopisteisiin tai karsintarankingiin: ratkaise järjestys käsin.") : null,
        h("div", { class: "scroll" }, h("table", null,
          h("thead", null, h("tr", null, h("th", null, ""), h("th", { class: "l" }, "Joukkue"), h("th", null, "O"), h("th", null, "V"), h("th", null, "T"), h("th", null, "H"), h("th", null, "M"), h("th", null, "P"))),
          h("tbody", null, rows))),
        h("p", { class: "note" }, grp.complete ? "Lohko on valmis." : "Taulukko päivittyy syötetyistä tuloksista (UEFAn säännöt: ensin keskinäiset ottelut, sitten maaliero ja tehdyt maalit)."),
        h("p", { class: "note legend" }, "O ottelut, V voitot, T tasapelit, H häviöt, M maalit, P pisteet"),
        orderEditor(g, grp, mx, reloadAll)),
      h("div", { class: "row" }, saveButton("Tallenna lohkon tulokset", () => rowObjs, reloadAll)));
    return h("div", null, chips, h("div", { class: "cols" }, left, right),
      h("div", { class: "row" }, saveButton("Tallenna lohkon tulokset", () => rowObjs, reloadAll)));
  }

  // Tuo useita tuloksia kerralla liittämällä teksti: yksi ottelu per rivi, esim. "12 2-1" tai "40 1-1 home".
  function importView(mx, reloadAll) {
    const canRes = state.perms.has("results");
    const area = h("textarea", { "aria-label": "Tulokset", placeholder: "1 2-1\n2 0-0\n37 1-1 home", disabled: !canRes });
    const out = h("div", { role: "status" });
    const run = h("button", { class: "btn primary", type: "button", disabled: !canRes }, "Tallenna tulokset");
    run.addEventListener("click", async () => {
      clearNode(out);
      const items = [], bad = [];
      area.value.split("\n").forEach((line, i) => {
        const t = line.trim();
        if (!t || t.startsWith("#")) return;
        const m = /^(\d{1,2})\s+(\d{1,2})\s*[-–:]\s*(\d{1,2})(?:\s+(home|away|koti|vieras))?$/i.exec(t);
        if (!m) { bad.push("Rivi " + (i + 1) + ": \"" + t + "\" ei ole muodossa ottelu koti-vieras [home|away]"); return; }
        const adv = m[4] ? (/^(home|koti)$/i.test(m[4]) ? "home" : "away") : "";
        const n = +m[1];
        if (n < 1 || n > 51) { bad.push("Rivi " + (i + 1) + ": ottelua " + n + " ei ole"); return; }
        items.push({ n, body: Object.assign({ homeGoals: +m[2], awayGoals: +m[3] }, adv ? { advance: adv } : {}) });
      });
      if (bad.length) { out.append(...bad.map((b) => h("div", { class: "err" }, b))); return; }
      if (!items.length) { out.append(h("div", { class: "err" }, "Ei tuotavia rivejä")); return; }
      run.disabled = true;
      let ok = 0;
      for (const it of items) {
        try { await api("PUT", "/admin/matches/" + it.n, it.body); ok++; out.append(h("div", { class: "okmsg" }, "✓ ottelu " + it.n + ": " + it.body.homeGoals + "–" + it.body.awayGoals)); }
        catch (e) {
          if (e.status === 401) { init(); return; }
          out.append(h("div", { class: "err" }, "✗ ottelu " + it.n + ": " + e.message));
        }
      }
      toast("Tuotu " + ok + " / " + items.length + " tulosta", ok === items.length);
      run.disabled = false;
      mx.dirty = true;
    });
    return h("div", null,
      h("p", { class: "note" }, "Liitä tulokset, yksi ottelu per rivi: ottelun numero, tulos ja pudotuspelien tasapelissä jatkoon selviävä (home tai away). Esimerkiksi \"12 2-1\" tai \"40 1-1 away\". Jokainen rivi tallennetaan samoilla säännöillä kuin käsin syötetty tulos (ottelun pitää olla alkanut)."),
      area, out, h("div", { class: "row" }, run, h("button", { class: "btn", type: "button", onclick: () => reloadAll() }, "Päivitä näkymä")));
  }

  function daysView(mx, reloadAll, redraw) {
    const days = [...new Set(mx.matches.map(dayKey))].sort();
    if (!state.aday || !days.includes(state.aday)) {
      const today = dayKey({ kickoff: Date.now() / 1000, kickoffConfirmed: true });
      state.aday = days.includes(today) ? today : (days.find((d) => mx.matches.some((m) => dayKey(m) === d && !m.result)) || days[0]);
    }
    const sel = h("select", { "aria-label": "Valitse päivä" }, days.map((d) => {
      const ms = mx.matches.filter((m) => dayKey(m) === d);
      const done = ms.filter((m) => m.result).length;
      const label = fmtMatchDay.format(new Date(d + "T00:00:00Z")) + " (" + done + "/" + ms.length + " tulosta)";
      return h("option", { value: d, selected: d === state.aday }, label);
    }));
    sel.addEventListener("change", () => { state.aday = sel.value; redraw(); });
    const rowObjs = [];
    const list = h("div");
    for (const m of mx.matches.filter((x) => dayKey(x) === state.aday).sort((a, b) => a.kickoff - b.kickoff || a.n - b.n)) {
      const r = resultRow(m, reloadAll); rowObjs.push(r); list.append(r.node);
    }
    return h("div", null,
      h("div", { class: "field" }, h("label", null, "Päivä"), sel),
      list,
      h("div", { class: "row" }, saveButton("Tallenna päivän tulokset", () => rowObjs, reloadAll)));
  }

  // Alkamisajat ja pudotuspelien joukkueet (setup-oikeus) sekä yksittäisen ottelun tarkka muokkaus.
  function scheduleView(mx, reloadAll) {
    const list = h("div");
    const filters = [["open", "Ilman tulosta"], ["all", "Kaikki"], ["group", "Lohkopelit"], ["ko", "Pudotuspelit"]];
    const chips = h("div", { class: "chips" });
    const draw = () => {
      clearNode(chips); clearNode(list);
      for (const [id, label] of filters) {
        chips.append(h("button", { class: "chip", type: "button", "aria-pressed": String(state.mfilter === id), onclick: () => { state.mfilter = id; draw(); } }, label));
      }
      const f = state.mfilter;
      const shown = mx.matches.filter((m) => f === "all" ? true : f === "open" ? !m.result : f === "group" ? m.stage === "group" : m.stage !== "group");
      if (!shown.length) list.append(h("p", { class: "note" }, "Ei otteluita tässä näkymässä."));
      for (const m of shown) list.append(matchEditor(m, reloadAll));
    };
    draw();
    return h("div", null,
      h("p", { class: "note" }, "Täällä asetetaan ottelujen alkamisajat (ensimmäisen ottelun aika määrää, milloin kaikki veikkaukset lukittuvat) ja pudotuspelien joukkueet. Ajat näytetään selaimesi aikavyöhykkeellä (" + tz + ")."),
      chips, list);
  }

  function matchEditor(m, reload) {
    const canRes = state.perms.has("results"), canSetup = state.perms.has("setup");
    const isKO = m.stage !== "group";
    const teamSel = (cur) => h("select", { "aria-label": "Joukkue", disabled: !canSetup || !isKO },
      h("option", { value: "" }, "–"),
      state.teams.map((t) => h("option", { value: t.code, selected: t.code === cur }, t.code + (t.name !== t.code ? " " + t.name : ""))));
    const hSel = teamSel(m.home ? m.home.code : ""), aSel = teamSel(m.away ? m.away.code : "");
    const kick = h("input", { type: "datetime-local", value: toLocal(m.kickoff), disabled: !canSetup, "aria-label": "Alkamisaika" });
    const goal = (v, label) => h("input", { type: "text", inputmode: "numeric", maxlength: 2, autocomplete: "off", value: v, disabled: !canRes, "aria-label": label });
    const hg = goal(m.result ? String(m.result.homeGoals) : "", "Kotimaalit");
    const ag = goal(m.result ? String(m.result.awayGoals) : "", "Vierasmaalit");
    for (const i of [hg, ag]) i.addEventListener("input", () => { i.value = i.value.replace(/\D/g, "").slice(0, 2); sync(); });
    let adv = m.result && m.result.advance ? m.result.advance : "";
    const name = (side) => (side ? side.name : "");
    const rHome = h("input", { type: "radio", name: "adv" + m.n, value: "home", checked: adv === "home", disabled: !canRes });
    const rAway = h("input", { type: "radio", name: "adv" + m.n, value: "away", checked: adv === "away", disabled: !canRes });
    for (const r of [rHome, rAway]) r.addEventListener("change", () => { adv = r.value; });
    const advRow = h("div", { class: "adv", hidden: true }, "Tasapeli, jatkoon:",
      h("label", null, rHome, name(m.home) || "koti"), h("label", null, rAway, name(m.away) || "vieras"));
    const tie = () => hg.value !== "" && ag.value !== "" && +hg.value === +ag.value;
    const sync = () => { advRow.hidden = !(isKO && tie()); };
    sync();

    async function send(body) {
      try {
        await api("PUT", "/admin/matches/" + m.n, body);
        toast("Ottelu " + m.n + " tallennettu");
        await reload();
      } catch (e) {
        if (e.status === 409 && e.code === "predictions_exist") {
          if (window.confirm(e.message + "\n\nPoistetaanko ottelun veikkaukset ja vaihdetaanko joukkueet?")) return send(Object.assign({}, body, { resetPredictions: true }));
          return;
        }
        handleErr(e);
      }
    }
    const save = h("button", { class: "btn primary small", type: "button" }, "Tallenna");
    save.addEventListener("click", async () => {
      const body = {};
      if (canSetup) {
        const ko = fromLocal(kick.value);
        if (ko && ko !== m.kickoff) body.kickoff = ko;
        if (isKO) {
          if (hSel.value !== (m.home ? m.home.code : "")) body.homeCode = hSel.value;
          if (aSel.value !== (m.away ? m.away.code : "")) body.awayCode = aSel.value;
        }
      }
      if (canRes && hg.value !== "" && ag.value !== "") {
        const cur = m.result;
        const changed = !cur || cur.homeGoals !== +hg.value || cur.awayGoals !== +ag.value || (tie() && cur.advance !== adv);
        if (changed) {
          if (isKO && tie() && !adv) { toast("Valitse jatkoon selviävä joukkue", false); return; }
          body.homeGoals = +hg.value; body.awayGoals = +ag.value;
          if (isKO && tie()) body.advance = adv;
        }
      }
      if (!Object.keys(body).length) { toast("Ei muutoksia", false); return; }
      save.disabled = true; await send(body); save.disabled = false;
    });

    const clear = m.result && canRes
      ? confirmButton("Poista tulos", "Vahvista tuloksen poisto", "danger", () => send({ clearResult: true }))
      : null;
    return h("div", { class: "acard" },
      h("div", { class: "head" },
        h("b", null, "Ottelu " + m.n),
        h("span", null, (m.home ? m.home.name : slotLabel(m.homeSlot)) + " – " + (m.away ? m.away.name : slotLabel(m.awaySlot))),
        h("span", { class: "note" }, m.venue + (m.group ? ", lohko " + m.group : "")),
        m.kickoffConfirmed ? null : h("span", { class: "badge warn" }, "aika vahvistamatta"),
        m.result ? h("span", { class: "badge" }, "tulos " + m.result.homeGoals + "–" + m.result.awayGoals) : null),
      h("div", { class: "fields" },
        h("label", null, "Alkamisaika", kick),
        isKO ? h("label", null, "Koti", hSel) : null,
        isKO ? h("label", null, "Vieras", aSel) : null,
        h("label", null, "Tulos (koti – vieras)", h("div", { class: "goals" }, hg, "–", ag))),
      advRow,
      h("div", { class: "rowbtns" }, save, clear));
  }

  /* ---------- joukkueet ---------- */
  async function viewTeams(box) {
    const can = state.perms.has("setup");
    const rows = state.teams.map((t) => {
      const inp = h("input", { type: "text", maxlength: 40, value: t.name, disabled: !can, "aria-label": "Joukkueen " + t.code + " nimi" });
      const btn = h("button", { class: "btn small", type: "button", disabled: !can }, "Tallenna");
      btn.addEventListener("click", async () => {
        btn.disabled = true;
        try { await api("PUT", "/admin/teams/" + t.code, { name: inp.value }); t.name = inp.value.trim(); toast(t.code + " tallennettu"); }
        catch (e) { handleErr(e); }
        btn.disabled = !can;
      });
      return h("tr", null, h("td", null, t.code), h("td", { class: "l" }, inp), h("td", null, btn));
    });
    box.append(h("h2", null, "Joukkueet"),
      h("p", { class: "lead" }, "Koodit A1–F4 ovat arvontapaikkoja. Kun arvonta on tehty, anna kullekin paikalle joukkueen nimi."),
      can ? null : h("p", { class: "note" }, "Sinulla ei ole setup-oikeutta, joten nimet ovat vain luettavissa."),
      h("div", { class: "card" }, h("div", { class: "scroll" }, h("table", null, h("tbody", null, rows)))));
  }

  /* ---------- kaavio ---------- */
  async function viewBracket(box) {
    const d = await api("GET", "/admin/bracket");
    const nm = (c) => { const t = state.teams.find((x) => x.code === c); return t && t.name !== c ? t.name + " (" + c + ")" : c; };
    const groups = Object.keys(d.groups).sort().map((g) => {
      const x = d.groups[g];
      return h("div", { class: "acard" },
        h("div", { class: "head" }, h("b", null, "Lohko " + g),
          x.complete ? null : h("span", { class: "badge" }, "kesken"),
          x.unresolved ? h("span", { class: "badge warn" }, "tasatilanne vaatii käsin ratkaisun") : null),
        x.complete ? h("ol", null, x.order.map((c) => h("li", null, nm(c)))) : h("p", { class: "note" }, "Kaikki lohkon ottelut eivät ole valmiita."));
    });
    const pairs = Object.keys(d.r16).sort((a, b) => a - b).map((n) => {
      const p = d.r16[n];
      return h("li", null, "Ottelu " + n + ": " + (p.home ? nm(p.home) : "?") + " – " + (p.away ? nm(p.away) : "?"));
    });
    const apply = h("button", { class: "btn primary", type: "button", disabled: !state.perms.has("results") || !pairs.length }, "Kirjoita ratkenneet parit otteluihin");
    apply.addEventListener("click", async () => {
      apply.disabled = true;
      try { await api("POST", "/admin/bracket/apply"); toast("Parit kirjoitettu"); render(); } catch (e) { handleErr(e); apply.disabled = false; }
    });
    box.append(h("h2", null, "Kaavio"),
      h("p", { class: "lead" }, "Lohkojen sijoitukset ja kahdeksannesvälierien parit lasketaan UEFAn sääntöjen mukaan syötetyistä tuloksista. Kurinpitopisteet ja karsintaranking eivät ole tiedossa, joten niihin asti ulottuvat tasatilanteet pitää ratkaista käsin."),
      d.thirds.qualifying ? h("p", null, "Neljä parasta kolmosta lohkoista: " + d.thirds.qualifying.split("").join(", ")) : null,
      d.thirds.unresolved ? h("p", { class: "err" }, "Parhaiden kolmosten raja on tasan, joten paikkoja ei voi täyttää automaattisesti.") : null,
      h("div", { class: "grid2" }, groups),
      h("h3", null, "Kahdeksannesvälierät"),
      pairs.length ? h("ul", null, pairs) : h("p", { class: "note" }, "Ei vielä ratkenneita pareja."),
      h("div", { class: "row" }, apply),
      thirdsEditor(d));
  }

  // Neljän parhaan kolmossijan käsin valinta, kun raja on tasan kaikilla tunnetuilla kriteereillä.
  function thirdsEditor(d) {
    if (!state.perms.has("results") || (!d.thirds.unresolved && !d.thirdsOverride)) return null;
    const boxes = "ABCDEF".split("").map((g) => h("label", { class: "chk" }, h("input", { type: "checkbox", value: g, checked: (d.thirds.qualifying || "").includes(g) }), " Lohko " + g));
    const save = h("button", { class: "btn small primary", type: "button" }, "Tallenna valinta");
    save.addEventListener("click", async () => {
      const groups = boxes.map((b) => b.firstChild).filter((i) => i.checked).map((i) => i.value);
      if (groups.length !== 4) { toast("Valitse täsmälleen neljä lohkoa", false); return; }
      try { await api("PUT", "/admin/thirds", { groups }); toast("Tallennettu"); render(); } catch (e) { handleErr(e); }
    });
    const clear = d.thirdsOverride ? confirmButton("Palauta automaattinen", "Vahvista", "danger", async () => {
      await api("DELETE", "/admin/thirds"); toast("Palautettu"); render();
    }) : null;
    return h("div", { class: "card" },
      h("h3", null, "Parhaat kolmoset käsin"),
      h("p", { class: "note" }, "Raja neljännen ja viidennen kolmossijan välillä on tasan kaikilla tunnetuilla kriteereillä. Valitse neljä lohkoa, joiden kolmonen jatkaa (UEFAn kurinpitopisteet ja karsintaranking ratkaisevat)."),
      h("div", { class: "row" }, boxes), h("div", { class: "rowbtns" }, save, clear));
  }

  /* ---------- maalikuningas ---------- */
  async function viewScorer(box) {
    const d = await api("GET", "/admin/scorers");
    const inp = h("input", { type: "text", id: "winners", maxlength: 200, value: d.winners.join(", "), autocomplete: "off" });
    const save = h("button", { class: "btn primary", type: "button" }, "Tallenna maalikuningas");
    save.addEventListener("click", async () => {
      const players = inp.value.split(",").map((x) => x.trim()).filter(Boolean);
      save.disabled = true;
      try { await api("PUT", "/admin/scorers", { players }); toast("Tallennettu"); } catch (e) { handleErr(e); }
      save.disabled = false;
    });
    box.append(h("h2", null, "Maalikuningas"),
      h("p", { class: "lead" }, "Kirjoita turnauksen maalikuningas. Tasatilanteessa voit antaa useita nimiä pilkulla erotettuna, ja jokainen veikkaaja saa pisteet. Nimien vertailu ei erota isoja ja pieniä kirjaimia tai aksentteja."),
      h("div", { class: "card accent" }, h("div", { class: "field" }, h("label", { for: "winners" }, "Maalikuningas"), inp), save),
      h("h3", null, "Suosituimmat veikkaukset"),
      d.predictions.length
        ? h("div", { class: "card" }, h("table", null, h("tbody", null, d.predictions.map((p) => h("tr", null, h("td", { class: "l" }, p.player), h("td", null, p.count))))))
        : h("p", { class: "note" }, "Ei veikkauksia."));
  }

  /* ---------- käyttäjät ---------- */
  async function viewUsers(box) {
    const q = h("input", { type: "text", class: "search", placeholder: "Hae nimimerkillä", "aria-label": "Hae käyttäjää", maxlength: 30 });
    const list = h("div");
    let t;
    const load = async () => {
      try {
        const d = await api("GET", "/admin/users?q=" + encodeURIComponent(q.value));
        clearNode(list);
        list.append(...(d.length ? d.map((u) => userRow(u, load)) : [h("p", { class: "note" }, "Ei tuloksia.")]));
      } catch (e) { handleErr(e); }
    };
    q.addEventListener("input", () => { clearTimeout(t); t = setTimeout(load, 300); });
    box.append(h("h2", null, "Käyttäjät"),
      h("p", { class: "lead" }, "Uusimmat ensin, enintään 100. Ylläpitäjiin ei voi kohdistaa näitä toimintoja: heidän oikeuksiaan hallitaan vain palvelimella."), q, list);
    await load();
  }

  function userRow(u, reload) {
    const act = (fn, msg) => async () => { await fn(); toast(msg); await reload(); };
    const nick = h("input", { type: "text", maxlength: 20, value: u.nickname, "aria-label": "Uusi nimimerkki", hidden: true });
    const rename = h("button", { class: "btn small", type: "button" }, "Nimeä uudelleen");
    rename.addEventListener("click", async () => {
      if (nick.hidden) { nick.hidden = false; nick.focus(); rename.textContent = "Tallenna nimi"; return; }
      try { await api("PUT", "/admin/users/" + u.id + "/nickname", { nickname: nick.value }); toast("Nimi vaihdettu"); await reload(); } catch (e) { handleErr(e); }
    });
    const buttons = u.admin ? [] : [
      nick, rename,
      h("button", { class: "btn small", type: "button", onclick: async () => { try { await act(() => api("PUT", "/admin/users/" + u.id + "/mute", { muted: !u.muted }), u.muted ? "Mykistys poistettu" : "Mykistetty")(); } catch (e) { handleErr(e); } } }, u.muted ? "Poista mykistys" : "Mykistä chat"),
      confirmButton("Kirjaa ulos", "Vahvista uloskirjaus", "", act(() => api("POST", "/admin/users/" + u.id + "/logout"), "Kirjattu ulos")),
      confirmButton("Poista käyttäjä", "Vahvista: poistetaan pysyvästi", "danger", act(() => api("DELETE", "/admin/users/" + u.id), "Käyttäjä poistettu")),
    ];
    return h("div", { class: "acard" },
      h("div", { class: "head" }, h("b", null, u.nickname),
        u.admin ? h("span", { class: "badge" }, "ylläpitäjä") : null,
        u.muted ? h("span", { class: "badge warn" }, "mykistetty") : null,
        h("span", { class: "note" }, "luotu " + fmtDT.format(new Date(u.createdAt * 1000)) + ", " + u.predictions + " veikkausta")),
      h("div", { class: "rowbtns" }, buttons));
  }

  /* ---------- chat ---------- */
  async function viewChat(box) {
    const list = h("div");
    const load = async () => {
      try {
        const d = await api("GET", "/admin/chat");
        clearNode(list);
        if (!d.length) { list.append(h("p", { class: "note" }, "Ei viestejä.")); return; }
        for (const m of d) {
          const row = h("div", { class: "msgrow" },
            h("span", { class: "note" }, fmtDT.format(new Date(m.at * 1000))),
            h("b", null, m.nickname),
            m.muted ? h("span", { class: "badge warn" }, "mykistetty") : null,
            m.reports ? h("span", { class: "badge warn" }, m.reports + (m.reports === 1 ? " ilmianto" : " ilmiantoa") + (m.reports >= 3 ? ", piilotettu" : "")) : null,
            h("span", { class: "txt" }, m.body),
            confirmButton("Poista viesti", "Vahvista", "danger", async () => { await api("DELETE", "/admin/chat/" + m.id); toast("Viesti poistettu"); await load(); }),
            m.admin || m.muted ? null : confirmButton("Mykistä kirjoittaja", "Vahvista mykistys", "", async () => { await api("PUT", "/admin/users/" + m.userId + "/mute", { muted: true }); toast("Kirjoittaja mykistetty"); await load(); }));
          list.append(row);
        }
      } catch (e) { handleErr(e); }
    };
    const words = h("input", { type: "text", id: "bw", maxlength: 600, placeholder: "sana1, sana2, sana3", "aria-label": "Kielletyt sanat" });
    try { words.value = (await api("GET", "/admin/chat/blocklist")).words.join(", "); } catch (e) { handleErr(e); }
    const saveWords = h("button", { class: "btn small", type: "button" }, "Tallenna kieltolista");
    saveWords.addEventListener("click", async () => {
      try {
        const r = await api("PUT", "/admin/chat/blocklist", { words: words.value.split(",").map((x) => x.trim()).filter(Boolean) });
        words.value = r.words.join(", "); toast("Kieltolista tallennettu");
      } catch (e) { handleErr(e); }
    });
    box.append(h("h2", null, "Chat"),
      h("p", { class: "lead" }, "200 viimeisintä viestiä, uusin ensin. Näkymä päivittyy 10 sekunnin välein. Viesti piilotetaan muilta, kun kolme pelaajaa on ilmiantanut sen."),
      h("div", { class: "card" },
        h("div", { class: "field" }, h("label", { for: "bw" }, "Kielletyt sanat (pilkulla erotettuina)"), words,
          h("span", { class: "note" }, "Viesti hylätään, jos se sisältää jonkin sanan osana, riippumatta isoista kirjaimista ja aksenteista. Valitse sanat harkiten: lyhyt sana voi osua myös ihan tavallisiin sanoihin.")),
        h("div", { class: "row" }, saveWords)),
      h("div", { class: "card" }, list));
    await load();
    state.timer = setInterval(() => { if (!document.hidden && list.isConnected) load(); }, 10000);
  }

  /* ---------- ylläpitäjät ja loki ---------- */
  async function viewAdmins(box) {
    const admins = await api("GET", "/admin/admins");
    const mfa = state.mfa || {};
    const mfaBtn = h("button", { class: "btn small primary", type: "button", disabled: !!mfa.enabled, onclick: () => renderMFASetup(false) }, mfa.enabled ? "Käytössä" : "Ota käyttöön");
    box.append(h("h2", null, "Ylläpitäjät"),
      h("div", { class: "card" }, h("h3", null, "Oma kaksivaiheinen kirjautuminen"),
        h("p", { class: "note" }, mfa.enabled ? "Kaksivaiheinen kirjautuminen on käytössä tilillesi." : "Suosittelemme ottamaan sen käyttöön: ylläpitotilillä on laajat oikeudet." + (mfa.required ? " Palvelin vaatii sen kaikilta ylläpitäjiltä." : "")),
        h("div", { class: "row" }, mfaBtn)),
      h("div", { class: "card" }, h("table", null,
        h("thead", null, h("tr", null, h("th", { class: "l" }, "Nimimerkki"), h("th", { class: "l" }, "Oikeudet"))),
        h("tbody", null, admins.map((a) => h("tr", null, h("td", { class: "l" }, a.nickname), h("td", { class: "l" }, a.perms.join(", ")))))))
      ,
      h("p", { class: "note" }, "Oikeuksia ei voi muuttaa tältä sivulta. Palvelimella: em2028 admin list | grant <nimimerkki> <all|oikeudet> | revoke <nimimerkki> [oikeudet]. Oikeuksien poisto vaikuttaa heti."));
    if (state.perms.has("audit")) {
      const log = await api("GET", "/admin/log");
      box.append(h("h3", null, "Ylläpitoloki"),
        h("div", { class: "card" }, h("div", { class: "scroll" }, h("table", { class: "log" },
          h("thead", null, h("tr", null, h("th", { class: "l" }, "Aika"), h("th", { class: "l" }, "Ylläpitäjä"), h("th", { class: "l" }, "Toiminto"), h("th", { class: "l" }, "Tiedot"))),
          h("tbody", null, log.map((r) => h("tr", null,
            h("td", null, fmtDT.format(new Date(r.at * 1000))), h("td", null, r.admin), h("td", null, r.action), h("td", null, r.detail))))))));
    } else {
      box.append(h("p", { class: "note" }, "Ylläpitolokin lukeminen vaatii audit-oikeuden."));
    }
  }

  init();
})();
