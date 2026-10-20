#!/usr/bin/env python3
"""Täysi päästä päähän -testi: rakentaa ja käynnistää OIKEAN Go-palvelimen väliaikaisella tietokannalla ja ajaa
koko polun selaimella: rekisteröinti, sähköpostin vahvistus (linkki luetaan palvelimen lokista), koko turnauksen
veikkaus, ylläpito-oikeudet, lukitus + satunnaiset viralliset tulokset (dev simulate), tulostaulu, toisen pelaajan
kaavion katselu, chat ja ylläpitonäkymät.

Vaatii: Go, Python 3, `pip install playwright && playwright install chromium`.
Ajo:    python e2e/run_e2e.py
"""
import os
import re
import sqlite3
import subprocess
import sys
import tempfile
import threading
import time
import urllib.error
import urllib.request

from playwright.sync_api import sync_playwright

ROOT = os.path.abspath(os.path.join(os.path.dirname(os.path.abspath(__file__)), ".."))
PORT = 8123
BASE = "http://127.0.0.1:%d" % PORT
PW = "pitkä-salasana-123"
GROUPS = "ABCDEF"


def wait_ready(timeout=40):
    end = time.time() + timeout
    while time.time() < end:
        try:
            if urllib.request.urlopen(BASE + "/healthz", timeout=2).read() == b"ok":
                return
        except Exception:  # noqa: BLE001
            time.sleep(0.4)
    raise RuntimeError("palvelin ei käynnistynyt ajoissa")


def link_for(log, email, page, timeout=15):
    """Lukee palvelimen lokista (MAIL_MODE=log) viimeisimmän viestin linkin: /<page>.html#t=..."""
    end = time.time() + timeout
    while time.time() < end:
        text = "".join(log)
        i = text.find("vastaanottaja=" + email)
        if i >= 0:
            m = re.search(r"http://[^\s]+/%s\.html#t=[A-Za-z0-9_-]+" % page, text[i:])
            if m:
                return m.group(0)
        time.sleep(0.3)
    raise RuntimeError("linkkiä %s ei löytynyt lokista (%s)" % (page, email))


def register(browser, nick, email):
    ctx = browser.new_context(viewport={"width": 1280, "height": 900})
    pg = ctx.new_page()
    pg.set_default_timeout(15000)
    pg.goto(BASE + "/")
    pg.click("button[role=tab]:has-text('Luo tunnus')")
    pg.fill("#nick", nick)
    pg.fill("#email", email)
    pg.fill("#pw", PW)
    pg.click("button.primary:has-text('Luo tunnus')")
    pg.wait_for_selector(".steps")
    return ctx, pg


def fill_groups(pg, letters):
    pg.click("button[role=tab]:has-text('Lohkot')")
    for g in letters:
        pg.click("button.gchip:has-text('%s')" % g)
        pg.wait_for_selector("h3:has-text('Lohko %s')" % g)
        pg.click("button:has-text('Täytä puuttuvat satunnaisesti')")
        pg.wait_for_selector("button.gchip:has-text('%s') small:has-text('6/6')" % g)


def fill_knockout(pg):
    pg.click("button[role=tab]:has-text('Pudotuspelit')")
    for n in range(37, 52):
        h = "[data-fid='p-%d-h']" % n
        pg.wait_for_selector(h + ":not([disabled])")
        pg.fill(h, "1")
        pg.fill("[data-fid='p-%d-a']" % n, "0")
        pg.wait_for_selector(".banner .progress:has-text('Veikattu %d / 51')" % (36 + n - 36))
    pg.wait_for_selector(".champion b")


def run(browser, binary, env, db_path, log):
    # --- pelaaja 1: koko turnaus
    ctx1, eeva = register(browser, "Eeva", "eeva@example.com")
    ver = browser.new_page()
    ver.goto(link_for(log, "eeva@example.com", "verify"))
    ver.wait_for_selector("h2:has-text('Sähköposti vahvistettu')")
    assert "#" not in ver.url, "tunnus pitää poistaa osoiterivilta"
    fill_groups(eeva, GROUPS)
    fill_knockout(eeva)
    eeva.click("button[role=tab]:has-text('Maalikuningas')")
    eeva.fill("#sname", "Harry Kane")
    eeva.click("button:has-text('Tallenna')")
    eeva.wait_for_selector("text=Tallennettu")
    print("OK   pelaaja 1: koko turnaus veikattu, mestari valittu")

    # --- pelaaja 2: osittainen
    ctx2, mikko = register(browser, "Mikko", "mikko@example.com")
    fill_groups(mikko, "AB")
    print("OK   pelaaja 2: lohkot A ja B")

    # --- toisen veikkauksia ei näe ennen lukitusta
    r = urllib.request.Request(BASE + "/api/players/Mikko", headers={"X-Requested-With": "em2028"})
    try:
        urllib.request.urlopen(r)
        raise AssertionError("ilman kirjautumista ei saa päästä")
    except urllib.error.HTTPError as e:
        assert e.code == 401

    # --- ylläpito-oikeudet palvelimella, kirjautuminen ylläpitoon
    subprocess.check_call([binary, "admin", "grant", "Eeva", "all"], env=env)
    adm = ctx1.new_page()
    adm.set_default_timeout(15000)
    adm.goto(BASE + "/admin.html")
    adm.wait_for_selector("h2:has-text('Yleiskatsaus'), #pw")
    if adm.locator("#pw").count():
        adm.fill("#email", "eeva@example.com")
        adm.fill("#pw", PW)
        adm.click("button.primary:has-text('Kirjaudu')")
    adm.wait_for_selector("h2:has-text('Yleiskatsaus')")
    print("OK   ylläpito: oikeudet myönnetty ja kirjautuminen toimii")

    # --- chat (uuden tunnuksen minuutin odotus ohitetaan siirtämällä luontiaikaa)
    con = sqlite3.connect(db_path)
    con.execute("UPDATE users SET created_at = created_at - 7200")
    con.commit()
    con.close()
    eeva.reload()
    eeva.click("nav.tabs button:has-text('Chat')")
    eeva.fill("#chatmsg", "Moi kaikki, onnea veikkaukseen!")
    eeva.click("button:has-text('Lähetä')")
    eeva.wait_for_selector(".msg.mine:has-text('Moi kaikki')")
    print("OK   chat: viesti lähti")

    # --- lukitus ja satunnaiset viralliset tulokset
    subprocess.check_call([binary, "dev", "simulate", "all", "lock"], env=env)
    eeva.reload()
    eeva.wait_for_selector(".banner.locked")
    eeva.click("nav.tabs button:has-text('Veikkaukset')")
    eeva.click("button[role=tab]:has-text('Lohkot')")
    assert eeva.locator("[data-fid='p-1-h']").is_disabled(), "lukituksen jälkeen kenttien pitää olla lukittu"
    print("OK   lukitus: veikkaukset lukittu, tulokset arvottu")

    # --- tulostaulu ja toisen pelaajan kaavio (välimuistin takia pieni uudelleenyritys)
    for _ in range(4):
        eeva.click("nav.tabs button:has-text('Tulostaulu')")
        eeva.wait_for_selector("td.rank")
        if eeva.locator("td.rank").count() >= 2:
            break
        time.sleep(5)
    assert eeva.locator("td.rank").count() >= 2
    eeva.click("button.linkbtn:has-text('Mikko')")
    eeva.wait_for_selector("h2.ph2:has-text('Mikko')")
    assert eeva.locator("[data-fid='p-1-h']").is_disabled()
    eeva.click("button:has-text('← Takaisin')")
    print("OK   tulostaulu ja pelaajan veikkausten katselu")

    # --- ylläpidon näkymät
    adm.click("nav.tabs button:has-text('Ottelut')")
    adm.wait_for_selector(".gchip small:has-text('6/6')")
    adm.wait_for_selector(".panel table tbody tr")
    adm.click("nav.tabs button:has-text('Kaavio')")
    adm.wait_for_selector("h3:has-text('Kahdeksannesvälierät')")
    adm.click("nav.tabs button:has-text('Käyttäjät')")
    adm.wait_for_selector(".acard:has-text('Mikko')")
    adm.click("nav.tabs button:has-text('Ylläpitäjät ja loki')")
    adm.wait_for_selector("text=Ylläpitoloki")
    print("OK   ylläpito: lohkotaulukot, kaavio, käyttäjät ja loki")


def main():
    tmp = tempfile.mkdtemp(prefix="em2028-e2e-")
    binary = os.path.join(tmp, "em2028")
    db_path = os.path.join(tmp, "e2e.db")
    print("Rakennetaan palvelin…")
    subprocess.check_call(["go", "build", "-o", binary, "."], cwd=ROOT)
    env = dict(os.environ, APP_SECRET="e2e-" + "x" * 48, COOKIE_SECURE="false", MAIL_MODE="log",
               ADDR="127.0.0.1:%d" % PORT, DB_PATH=db_path, BASE_URL=BASE, ALLOW_SIMULATE="yes")
    srv = subprocess.Popen([binary], env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, bufsize=1)
    log = []
    threading.Thread(target=lambda: [log.append(line) for line in srv.stdout], daemon=True).start()
    code = 0
    try:
        wait_ready()
        with sync_playwright() as p:
            browser = p.chromium.launch()
            run(browser, binary, env, db_path, log)
            browser.close()
        print("E2E OK")
    except Exception as e:  # noqa: BLE001
        code = 1
        print("E2E VIRHE:", repr(e))
        print("--- palvelimen loki (loppu) ---")
        print("".join(log[-25:]))
    finally:
        srv.terminate()
        try:
            srv.wait(5)
        except subprocess.TimeoutExpired:
            srv.kill()
    sys.exit(code)


if __name__ == "__main__":
    main()
