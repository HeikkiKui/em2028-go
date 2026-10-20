"use strict";
// Sähköpostilinkkien sivut (verify.html, reset.html). Tunnus on osoitteen #-osassa, joten se ei
// lähde palvelimelle lokeihin eikä Referer-otsakkeeseen. Se poistetaan osoiterivilta heti.
(() => {
  const app = document.getElementById("app");
  const m = /(?:^#|&)t=([A-Za-z0-9_-]{20,100})/.exec(location.hash);
  const token = m ? m[1] : "";
  history.replaceState(null, "", location.pathname);

  function h(tag, props, ...kids) {
    const el = document.createElement(tag);
    for (const [k, v] of Object.entries(props || {})) {
      if (v === false || v == null) continue;
      if (k === "class") el.className = v;
      else if (k === "disabled") el.disabled = !!v;
      else if (k.startsWith("on") && typeof v === "function") el.addEventListener(k.slice(2), v);
      else el.setAttribute(k, v === true ? "" : String(v));
    }
    for (const kid of kids.flat(Infinity)) { if (kid != null && kid !== false) el.append(kid.nodeType ? kid : document.createTextNode(String(kid))); }
    return el;
  }
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
  async function post(path, body) {
    const res = await fetch("/api" + path, { method: "POST", credentials: "same-origin",
      headers: { "X-Requested-With": "em2028", "Content-Type": "application/json" }, body: JSON.stringify(body) });
    let data = null; try { data = await res.json(); } catch { /* tyhjä */ }
    if (!res.ok) throw new Error((data && data.error) || "Jokin meni pieleen");
    return data;
  }
  const home = () => h("p", null, h("a", { class: "btn primary", href: "/" }, "Takaisin veikkaukseen"));
  const set = (...nodes) => { while (app.firstChild) app.removeChild(app.firstChild); app.append(h("section", { class: "view" }, nodes)); };

  async function verify() {
    if (!token) { set(h("h2", null, "Virheellinen linkki"), h("p", { class: "lead" }, "Linkistä puuttuu tunnus. Avaa viesti uudelleen tai pyydä uusi linkki kirjautuneena."), home()); return; }
    try {
      await post("/email/verify", { token });
      set(h("h2", null, "Sähköposti vahvistettu"), h("p", { class: "lead" }, "Kiitos! Voit nyt kirjoittaa chattiin."), home());
    } catch (e) {
      set(h("h2", null, "Vahvistus ei onnistunut"), h("p", { class: "err" }, e.message), home());
    }
  }

  function reset() {
    if (!token) { set(h("h2", null, "Virheellinen linkki"), h("p", { class: "lead" }, "Pyydä uusi palautuslinkki kirjautumissivulta."), home()); return; }
    const pw = passwordField("pw", "new-password", { minlength: 10 });
    const err = h("div", { class: "err", role: "alert" });
    const btn = h("button", { class: "btn primary", type: "button" }, "Tallenna uusi salasana");
    const go = async () => {
      err.textContent = ""; btn.disabled = true;
      try {
        await post("/password/reset", { token, password: pw.input.value });
        set(h("h2", null, "Salasana vaihdettu"), h("p", { class: "lead" }, "Kaikki laitteet on kirjattu ulos. Kirjaudu nyt uudella salasanallasi."), home());
        } catch (e) { err.textContent = e.message; btn.disabled = false; }
    };
    btn.addEventListener("click", go);
    pw.input.addEventListener("keydown", (e) => { if (e.key === "Enter") go(); });
    set(h("h2", null, "Valitse uusi salasana"),
      h("div", { class: "card accent" },
        h("div", { class: "field" }, h("label", { for: "pw" }, "Uusi salasana"), pw.node, h("span", { class: "note" }, "Vähintään 10 merkkiä. Pitkä lause on parempi kuin monimutkainen sana.")),
        err, btn));
    pw.input.focus();
  }

  async function unsubscribe() {
    if (!token) { set(h("h2", null, "Virheellinen linkki"), h("p", { class: "lead" }, "Avaa viestin peruutuslinkki uudelleen tai poista muistutukset Tili-välilehdeltä."), home()); return; }
    try {
      await post("/unsubscribe", { token });
      set(h("h2", null, "Muistutukset poistettu"), h("p", { class: "lead" }, "Et saa enää veikkausmuistutuksia. Voit kytkeä ne takaisin Tili-välilehdeltä."), home());
    } catch (e) {
      set(h("h2", null, "Peruutus ei onnistunut"), h("p", { class: "err" }, e.message), home());
    }
  }

  const page = document.body.dataset.page;
  if (page === "verify") verify(); else if (page === "unsubscribe") unsubscribe(); else reset();
})();
