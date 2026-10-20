# EM 2028 -veikkaus (Go + SQLite)

## Komennot
- Testit: `go vet ./... && go test ./...`
- Käynnistys: `APP_SECRET=$(cat .secret) COOKIE_SECURE=false MAIL_MODE=log go run .`
- Selaintestit: `python e2e/ui/ui_tests.py`, täysi E2E: `python e2e/run_e2e.py`

## Säännöt
- Käyttöliittymän tekstit suomeksi, selkokielellä. Yksinkertaisuus on tärkeintä.
- Tiukka CSP: ei inline-skriptejä eikä style-attribuutteja. Käytä DOM-solmuja ja textContentia, ei innerHTML:ää.
- Kaikki SQL parametrisoituna. Tietokantamuutokset vain `migrations`-listan loppuun (db.go), vanhoja ei muokata.
- Älä koskaan tulosta tai tallenna salasanoja, sähköposteja selkokielisenä tai istuntotunnuksia lokiin.
- Älä aja `dev simulate` tai `purge-players` -komentoja muuhun kuin testitietokantaan.
- Älä koske tiedostoihin `.secret` ja `data/`. Älä committaa niitä.
- Tarkista ennen valmista: testit menevät läpi. Kerro, mitä et ole ajanut.

## Rakenne
Ks. README.md ("Rakenne"). Pelaajan käyttöliittymä: web/app.js, ylläpito: web/admin.js + admin.go.
