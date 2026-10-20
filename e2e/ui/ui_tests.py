#!/usr/bin/env python3
"""Käyttöliittymätestit oikealla selaimella (Chromium) testipalvelinta vasten: ei vaadi Go:ta.
Ajo:  pip install playwright && playwright install chromium && python e2e/ui/ui_tests.py"""
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import mock_server  # noqa: E402
from playwright.sync_api import sync_playwright  # noqa: E402

BASE = "http://127.0.0.1:8099"
PW = "pitkä-salasana-123"


def new_page(b, **kw):
    ctx = b.new_context(**kw)
    pg = ctx.new_page()
    pg.set_default_timeout(8000)
    errs = []
    pg.on("pageerror", lambda e: errs.append("PAGEERROR " + str(e)))
    pg.on("console", lambda m: errs.append("CONSOLE " + m.text)
          if m.type in ("error", "warning") and not any(c in m.text for c in ("401", "403", "400")) else None)
    return pg, errs


def register(pg):
    pg.goto(BASE + "/")
    pg.click("button[role=tab]:has-text('Luo tunnus')")
    pg.fill("#nick", "Testaaja")
    pg.fill("#email", "testi@example.com")
    pg.fill("#pw", PW)
    pg.click("button.primary:has-text('Luo tunnus')")
    pg.wait_for_selector(".steps")


def test_password_toggle_everywhere(b):
    for label, url, pre in (("rekisteröinti", "/", "reg"), ("kirjautuminen", "/", ""), ("ylläpito", "/admin.html", ""),
                            ("palautus", "/reset.html#t=AAAAAAAAAAAAAAAAAAAAAAAAAAAA", "")):
        pg, errs = new_page(b)
        pg.goto(BASE + url)
        if pre:
            pg.click("button[role=tab]:has-text('Luo tunnus')")
        pg.wait_for_selector("#pw")
        pg.fill("#pw", PW)
        assert pg.get_attribute("#pw", "type") == "password"
        pg.click(".pw button")
        assert pg.get_attribute("#pw", "type") == "text", label
        pg.click(".pw button")
        assert pg.get_attribute("#pw", "type") == "password", label
        assert not errs, errs


def test_register_and_predict(b):
    for vp in ({"width": 1280, "height": 900}, {"width": 390, "height": 800}):
        mock_server.reset()
        pg, errs = new_page(b, viewport=vp)
        register(pg)
        pg.fill("[data-fid='p-1-h']", "2")
        pg.fill("[data-fid='p-1-a']", "1")
        pg.wait_for_timeout(900)
        assert pg.input_value("[data-fid='p-1-h']") == "2"
        pg.click("button.gchip:has-text('B')")
        pg.wait_for_selector("h3:has-text('Lohko B')")
        pg.click("button:has-text('Täytä puuttuvat satunnaisesti')")
        pg.wait_for_selector(".banner .progress:has-text('Veikattu 7 / 51')")
        for sub in ("Pudotuspelit", "Maalikuningas", "Lohkot"):
            pg.click("button[role=tab]:has-text('%s')" % sub)
            pg.wait_for_timeout(200)
        for t in ("Tulostaulu", "Chat", "Tili"):
            pg.click("nav.tabs button:has-text('%s')" % t)
            pg.wait_for_timeout(200)
        assert not errs, errs


def test_player_features(b):
    mock_server.reset()
    pg, errs = new_page(b, viewport={"width": 1280, "height": 900}, accept_downloads=True)
    register(pg)
    pg.click("nav.tabs button:has-text('Tili')")
    pg.wait_for_selector("#rem")
    assert pg.is_checked("#rem")
    pg.uncheck("#rem")
    pg.wait_for_selector("text=Tallennettu")
    with pg.expect_download() as dl:
        pg.click("button:has-text('Lataa tietoni')")
    assert dl.value.suggested_filename == "em2028-tietoni.json"
    pg.request.get(BASE + "/api/_lock")
    pg.reload()
    pg.wait_for_selector(".steps")
    pg.click("button[role=tab]:has-text('Pudotuspelit')")
    pg.click("button[role=tab]:has-text('Kaavio')")
    pg.wait_for_selector(".tbox")
    assert pg.locator(".tbox").count() == 15
    pg.click("nav.tabs button:has-text('Tulostaulu')")
    pg.wait_for_selector("td.rank")
    assert "▲2" in pg.locator("small.up").inner_text().replace(" ", "")
    pg.click("button.linkbtn:has-text('Maija')")
    pg.wait_for_selector("h2.ph2")
    assert pg.locator("[data-fid='p-1-h']").is_disabled()
    pg.click("button:has-text('← Takaisin')")
    pg.click("nav.tabs button:has-text('Chat')")
    pg.wait_for_selector("button.rep")
    pg.click("button.rep")
    pg.wait_for_selector("button.rep:has-text('Ilmiannettu')")
    assert not errs, errs


def test_admin_2fa_overrides_import(b):
    mock_server.reset()
    pg, errs = new_page(b, viewport={"width": 1280, "height": 1000})
    pg.goto(BASE + "/admin.html")
    pg.request.post(BASE + "/api/_admin", data='{"mfa":"on"}', headers={"Content-Type": "application/json"})
    pg.reload()
    pg.wait_for_selector("#pw")
    pg.fill("#email", "admin@example.com")
    pg.fill("#pw", PW)
    pg.click("button.primary:has-text('Kirjaudu')")
    pg.wait_for_selector("#mfa")
    pg.fill("#mfa", "000000")
    pg.click("button:has-text('Vahvista')")
    pg.wait_for_selector(".err:has-text('ei täsmää')")
    pg.fill("#mfa", "123456")
    pg.click("button:has-text('Vahvista')")
    pg.wait_for_selector("h2:has-text('Yleiskatsaus')")
    pg.click("nav.tabs button:has-text('Ottelut')")
    pg.wait_for_selector("summary:has-text('Tasatilanne')")
    pg.locator("details select").first.select_option("A2")
    assert pg.locator("details select").nth(1).input_value() == "A1"
    pg.click("button:has-text('Tallenna järjestys')")
    pg.wait_for_selector("summary:has-text('asetettu käsin')")
    pg.click("button[role=tab]:has-text('Tuo tulokset')")
    pg.fill("textarea", "1 2-1\nrikki rivi\n")
    pg.click("button:has-text('Tallenna tulokset')")
    pg.wait_for_selector(".err:has-text('Rivi 2')")
    pg.fill("textarea", "1 2-1\n37 1-1 home\n")
    pg.click("button:has-text('Tallenna tulokset')")
    pg.wait_for_selector("text=✓ ottelu 37")
    pg.click("nav.tabs button:has-text('Käyttäjät')")
    pg.wait_for_selector(".acard")
    names = [x.inner_text() for x in pg.locator(".acard .head b").all()]
    assert names == ["Maija", "Pekka", "Admin"], names
    assert "object" not in pg.inner_text("main")
    pg.click("nav.tabs button:has-text('Chat')")
    pg.wait_for_selector("text=2 ilmiantoa")
    assert pg.input_value("#bw") == "kakka"
    assert not errs, errs


def test_2fa_setup_and_static_pages(b):
    mock_server.reset()
    pg, errs = new_page(b)
    pg.goto(BASE + "/admin.html")
    pg.request.post(BASE + "/api/_admin", data='{"mfa":"off"}', headers={"Content-Type": "application/json"})
    pg.reload()
    pg.wait_for_selector("#pw")
    pg.fill("#email", "a@example.com")
    pg.fill("#pw", PW)
    pg.click("button.primary:has-text('Kirjaudu')")
    pg.wait_for_selector("h2:has-text('Yleiskatsaus')")
    pg.click("nav.tabs button:has-text('Ylläpitäjät ja loki')")
    pg.click("button:has-text('Ota käyttöön')")
    pg.click("button:has-text('Aloita')")
    pg.wait_for_selector(".keybox")
    pg.fill("#mfa", "123456")
    pg.click("button:has-text('Vahvista ja ota käyttöön')")
    pg.wait_for_selector("text=Kaksivaiheinen kirjautuminen on käytössä")
    pg2, e2 = new_page(b)
    pg2.goto(BASE + "/unsubscribe.html#t=" + "a" * 48)
    pg2.wait_for_selector("h2:has-text('Muistutukset poistettu')")
    assert pg2.url.endswith("/unsubscribe.html"), "tunnus pitää poistaa osoiterivilta"
    pg3, _ = new_page(b)
    pg3.goto(BASE + "/manifest.json")
    assert "EM 2028" in pg3.inner_text("body")
    assert not errs and not e2, (errs, e2)


TESTS = [test_password_toggle_everywhere, test_register_and_predict, test_player_features,
         test_admin_2fa_overrides_import, test_2fa_setup_and_static_pages]

if __name__ == "__main__":
    mock_server.serve(8099)
    failed = 0
    with sync_playwright() as p:
        browser = p.chromium.launch()
        for t in TESTS:
            mock_server.reset()
            try:
                t(browser)
                print("OK   ", t.__name__)
            except Exception as e:  # noqa: BLE001
                failed += 1
                print("VIRHE", t.__name__, "->", repr(e)[:300])
        browser.close()
    print("%d / %d testiä läpi" % (len(TESTS) - failed, len(TESTS)))
    sys.exit(1 if failed else 0)
