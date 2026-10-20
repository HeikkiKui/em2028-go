# EM 2028 -veikkaus (Go + SQLite)

Veikkauspalvelu UEFA EURO 2028 -kisoihin: ottelutulokset, maalikuningas, yhteinen tulostaulu, chat ja ylläpitosivu.

**Tunnus: sähköposti + salasana.** Helppo pelaajille, ja tiedot suojataan parhaalla käytännöllä: salasana tallennetaan vain argon2id-tiivisteenä (lisäksi palvelimen salaisuudella höystettynä), sähköposti vain salattuna (AES-256-GCM), ja kumpikaan ei näy muille pelaajille eikä ylläpitäjille. Vahvistus ja salasanan palautus tehdään sähköpostilinkillä. IP-osoitteita ei tallenneta.

## Käynnistys paikallisesti

Vaatii Go 1.22+. Ensimmäisellä kerralla haetaan riippuvuudet (luo `go.sum`):

```sh
go mod tidy
go vet ./... && go test ./...

export APP_SECRET=$(openssl rand -hex 32)   # TALLENNA TÄMÄ, katso alla
COOKIE_SECURE=false MAIL_MODE=log go run .  # http://localhost:8080
```

`COOKIE_SECURE=false` ja `MAIL_MODE=log` ovat vain paikalliseen kehitykseen: sähköpostit (vahvistus- ja palautuslinkit) tulostuvat palvelimen lokiin eikä oikeaa viestiä lähde. Tuotannossa käytä HTTPS:ää ja SMTP:tä (ks. alla).

## Ympäristömuuttujat

| Muuttuja | Pakollinen | Kuvaus |
|---|---|---|
| `APP_SECRET` | kyllä | Vähintään 32 merkkiä. Salaisuus, josta johdetaan salasanojen pepper, sähköpostien salausavain ja istuntojen tiivisteavain. |
| `DB_PATH` | ei | Oletus `data/em2028.db`. |
| `ADDR` | ei | Oletus `:8080`. |
| `COOKIE_SECURE` | ei | Oletus `true`. Aseta `false` vain http-kehityksessä. |
| `BASE_URL` | tuotannossa | Julkinen osoite, esim. `https://veikkaus.example.com` (sähköpostilinkkeihin). Tuotannossa pakko olla `https://`. |
| `SMTP_HOST` | tuotannossa | Lähtevän postin palvelin. Ilman sitä on pakko asettaa `MAIL_MODE=log` (vain kehitys). |
| `SMTP_PORT` | ei | Oletus `587` (STARTTLS pakollinen). `465` käyttää suoraa TLS:ää. |
| `SMTP_USER`, `SMTP_PASS` | yleensä | SMTP-tunnukset. |
| `MAIL_FROM` | SMTP:llä | Lähettäjä, esim. `EM 2028 <no-reply@veikkaus.example.com>`. |
| `REQUIRE_ADMIN_2FA` | ei | `true` vaatii kaikilta ylläpitäjiltä kaksivaiheisen kirjautumisen (TOTP). Suositeltu tuotannossa. |
| `NOTIFY_WEBHOOK_URL` | ei | `https://`-osoite Discord/Slack-yhteensopivalle webhookille: ilmoitus uudesta tuloksesta ja lähetetyistä muistutuksista. Ei henkilötietoja. |
| `PWNED_CHECK` | ei | `true` tarkistaa rekisteröityessä, onko salasana vuotanut (Have I Been Pwned, k-anonymiteetti). Oletus pois. |
| `TRUST_PROXY` | ei | `true` vain, jos edessä on oma käänteinen välityspalvelin (Caddy, nginx). Silloin IP luetaan `X-Forwarded-For`-listan viimeisestä arvosta. |

**Tärkeää:** jos `APP_SECRET` vaihtuu tai katoaa, **kaikki salasanat lakkaavat toimimasta ja kaikki tallennetut sähköpostit muuttuvat lukukelvottomiksi** (eikä niitä voi palauttaa). Säilytä se salasanahallinnassa ja varmuuskopioi se erikseen tietokannasta. Arvon vaihtamista (rotaatiota) ei ole toteutettu; se vaatisi käyttäjien pakotetun salasanan palautuksen.

## Ylläpito

Ylläpitosivu on osoitteessa **`/admin.html`**. Se ei ole linkitetty pelaajille, mutta pääsynhallinta on aina palvelimella.

### Oikeuksien myöntäminen (käsin, palvelimella)

Ylläpitäjät ovat tavallisia pelaajatunnuksia, joille myönnetään oikeuksia. Oikeuksia **ei voi myöntää verkon kautta**: vain se, jolla on pääsy palvelimeen ja tietokantaan, voi tehdä sen. Tunnuksen pitää ensin luoda normaalisti sivulla (sähköposti + salasana).

```sh
em2028 admin list
em2028 admin grant  Matti all                     # kaikki oikeudet
em2028 admin grant  Liisa results,moderate        # vain osa
em2028 admin revoke Liisa moderate                # poistaa yhden oikeuden
em2028 admin revoke Matti                         # poistaa kaikki
em2028 admin delete-user Pekka                    # poistaa käyttäjän ja kaikki hänen tietonsa
CONFIRM_DELETE_ADMIN=yes em2028 admin delete-user Matti   # ylläpitäjän poisto vaatii vahvistuksen
```

Dockerissa: `docker exec em2028 /em2028 admin grant Matti all`. Komento tarvitsee vain `DB_PATH`:n, ei `APP_SECRET`:iä. Oikeuksien poisto vaikuttaa heti, koska oikeudet luetaan tietokannasta joka pyynnöllä.

| Oikeus | Sallii |
|---|---|
| `results` | ottelujen tulokset, tuloksen poisto, maalikuningas, kaavion täyttö |
| `setup` | joukkueiden nimet, ottelujen alkamisajat, pudotuspelien joukkueet |
| `moderate` | käyttäjien haku, uudelleennimeäminen, chat-mykistys, uloskirjaus ja poisto, chat-viestien poisto |
| `audit` | ylläpitolokin lukeminen |

Jokainen ylläpitäjä näkee Yleiskatsauksen, Ottelut, Joukkueet, Kaavio ja Ylläpitäjät-näkymät. Muut välilehdet näkyvät oikeuksien mukaan, ja palvelin tarkistaa oikeuden jokaisessa kutsussa.

### Turvasäännöt

- **Tuore kirjautuminen:** ylläpitotoiminnot vaativat kirjautumisen (sähköposti + salasana) viimeisen 12 tunnin aikana. Sivu pyytää sen tarvittaessa uudelleen.
- **Ylläpitäjiin ei voi kohdistaa moderointia:** moderaattori ei voi mykistää, nimetä uudelleen, kirjata ulos tai poistaa toista ylläpitäjää. Ylläpitäjien hallinta tapahtuu vain palvelimella.
- **Ylläpitoloki:** kaikki muutokset (myös komentorivin grant/revoke) kirjataan: aika, ylläpitäjän nimimerkki, toiminto ja lyhyt kuvaus. Lokiin ei tallenneta chat-viestien sisältöä eikä IP-osoitteita.
- **Tulosten suojat:** tulosta ei voi syöttää ennen ottelun alkamisaikaa (tulos lukitsisi veikkaukset), eikä alkamisaikaa voi muuttaa ottelulle, jolla on tulos. Jos pudotuspelin joukkueet vaihdetaan ottelulle, jolla on jo veikkauksia, sivu kysyy ensin vahvistuksen, koska veikkaukset nollataan.
- **Nopeusrajoitus:** 3 ylläpitotoimintoa sekunnissa käyttäjää kohti.

### Kisojen kulku

1. Arvonnan jälkeen: **Joukkueet**-näkymässä nimet paikoille A1–F4. Isäntämaiden oletuspaikat (A1 Wales, B1 Englanti, D1 Pohjois-Irlanti, E1 Irlanti, F1 Skotlanti) on varattu, jos ne pääsevät mukaan, joten korjaa tarvittaessa (esim. Pohjois-Irlanti).
2. Kun UEFA vahvistaa kellonajat: **Ottelut**-näkymässä alkamisajat. **Ensimmäisen ottelun aika määrää, milloin kaikki veikkaukset lukittuvat.** Siihen asti oletus on avausottelun päivän alku (UTC).
3. Ottelun jälkeen: **Ottelut**-näkymän **Lohkot**-välilehdellä on lohkovalitsin, virallinen lohkotaulukko vasemmalla ja lohkon ottelut oikealla. Kirjoita tulokset ja paina **Tallenna lohkon tulokset** (tallentaa kerralla kaikki muuttuneet). **Päivät**-välilehdellä sama koko pelipäivän otteluille. Pudotuspelien tasapelissä valitse jatkoon selviävä joukkue, ja voittaja siirtyy automaattisesti seuraavaan otteluun. **Pudotuspelit ja ajat** -välilehdellä asetetaan alkamisajat ja pudotuspelien joukkueet.
   Pelaajien pisteet lasketaan aina näistä virallisista tuloksista, ja ne päivittyvät heti, jos tulosta korjataan.
4. Lohkovaiheen jälkeen: **Kaavio**-näkymä näyttää lopputaulukot ja kahdeksannesvälierien parit, ja painike kirjoittaa varmasti ratkenneet parit otteluihin.
5. Turnauksen lopuksi: **Maalikuningas**-näkymässä voittaja (tai useita, jos tasan).

### Kaavio ja UEFAn säännöt

Lohkojen lopputaulukot lasketaan UEFA EURO 2026–28 -sääntöjen mukaan (art. 19.04, 20.01 ja 21.03–21.04). Tasapisteissä sovelletaan ensin keskinäisiä otteluita, sitten koko lohkon maalieroa ja tehtyjä maaleja. Neljän parhaan kolmossijan sijoittelu käyttää virallista 15 rivin taulukkoa.

**Rajoitus:** kurinpitopisteet (art. 20.01 g), karsintojen kokonaisranking (h) ja tasatilanteen rangaistuspotkukilpailu (art. 20.02) eivät ole palvelimen tiedossa. Jos tasatilanne ulottuu niihin, kaavio merkitsee lohkon ratkaisemattomaksi eikä täytä sitä. Aseta silloin joukkueet käsin Ottelut-näkymässä. Jos tuloksen korjaus muuttaisi jatkoon selviävää joukkuetta ja seuraavalle ottelulle on jo veikkauksia, joukkuetta ei vaihdeta automaattisesti: korjaa se käsin (veikkaukset nollataan vahvistuksen jälkeen).

## Sähköposti (SMTP)

Palvelu lähettää kolmenlaisia viestejä: sähköpostin vahvistus (24 h), salasanan palautuslinkki (1 h, kertakäyttöinen) ja ilmoitus salasanan vaihdosta. Tarvitset SMTP-palvelun: UpCloudilla ei ole omaa lähtevän postin palvelua, joten käytä erillistä (esim. Postmark, Mailgun, Amazon SES, Brevo tai oman verkkotunnuksesi postipalvelu). Palvelu hylkää selkokielisen SMTP-yhteyden: STARTTLS tai TLS on pakollinen.

Jotta viestit eivät päädy roskapostiin, aseta lähettäjän verkkotunnukselle **SPF-, DKIM- ja DMARC-tietueet** palveluntarjoajan ohjeiden mukaan. Linkit sisältävät kertakäyttöisen tunnuksen osoitteen `#`-osassa, joten se ei päädy palvelimen lokeihin tai Referer-otsakkeisiin.

Käyttäjä voi käyttää peliä myös ilman vahvistettua sähköpostia, mutta chattiin kirjoittaminen vaatii vahvistuksen. Tämä hidastaa roskapostia ja kertakäyttötunnuksia.

## Testaus: satunnaiset viralliset tulokset

Jotta pisteytystä ja tulostaulua voi kokeilla heti ilman todellisia otteluita, on testikomento, joka arpoo viralliset tulokset suoraan tietokantaan. **Se ei koske ottelujen aikoihin, joten veikkaukset pysyvät auki.** Se vaatii `ALLOW_SIMULATE=yes`, eikä sitä saa ajaa tuotantotietokannalle.

```sh
ALLOW_SIMULATE=yes go run . dev simulate all          # lohkot, pudotuspelit, mestari ja maalikuningas
ALLOW_SIMULATE=yes go run . dev simulate groups       # vain lohkopelit
ALLOW_SIMULATE=yes go run . dev simulate all lock     # lisäksi lukitsee veikkaukset (kokeile lukituksen jälkeistä tilaa)
ALLOW_SIMULATE=yes go run . dev clear                 # poistaa tulokset, palauttaa ajat ja avaa veikkaukset
```

Lukituksen jälkeiset näkymät (toisten pelaajien veikkaukset ja kaaviot) aukeavat vasta `lock`-vaihtoehdolla. `clear` säilyttää pelaajien veikkaukset, joten voit toistaa testin.

## Veikkaus ja pisteytys

**Kaikki veikkaukset ovat auki ja vapaasti muokattavissa, kunnes turnauksen ensimmäinen ottelu alkaa. Sen jälkeen kaikki lukittuu kerralla** (ottelutulokset, pudotuspelit ja maalikuningas). Lukitus tarkistetaan palvelimella: se on aikaisin ottelun alkamisaika tietokannassa.

Koska pudotuspelien joukkueet eivät ole tiedossa ennen turnausta, pelaaja veikkaa oman kaavionsa: lohkotulokset -> lohkojen sijoitukset (UEFAn säännöt, sama laskenta kuin oikeille tuloksille) -> kahdeksannesvälierät (virallinen kolmossijataulukko) -> finaaliin ja mestariin. Kaavio lasketaan palvelimella pelaajan veikkauksista. Jos pelaaja muuttaa lohkotulosta ja kaavio muuttuu, vanhentuneet pudotuspeliveikkaukset poistetaan automaattisesti ja veikattavaksi tulee uusi pari.

| Mitä | Pisteet |
|---|---|
| Täsmälleen oikea tulos | 3 |
| Oikea voittaja tai tasapeli, väärä tulos | 1 |
| Pudotuspeli: oikea jatkoon selviäjä | +1 |
| Oikein veikattu joukkue kahdeksannesvälierissä (16) | 2 / joukkue |
| Oikein veikattu joukkue neljännesvälierissä (8) | 3 / joukkue |
| Oikein veikattu joukkue välierissä (4) | 4 / joukkue |
| Oikein veikattu finalisti (2) | 5 / joukkue |
| Oikea mestari | 10 |
| Oikea maalikuningas | 10 |

Pudotuspelin ottelupisteet (täsmätulos, voittaja, jatkaja) saa vain, jos pelaajan veikkaama pari on sama kuin oikea ottelu. Jatkopisteet lasketaan erikseen sen mukaan, ketkä oikeasti pääsevät kullekin kierrokselle. Tasapisteissä ratkaisee täsmätulosten määrä. Tulos tarkoittaa varsinaisen peliajan (90 min) tulosta. Muuta arvoja tiedostossa `scoring.go`.

Muiden pelaajien veikkaukset näkyvät lohkopeleistä vasta lukituksen jälkeen. Kun ylläpito siirtää alkamisaikaa, veikkauksia ei voi avata uudelleen lukituksen jälkeen: palvelin hylkää muutoksen, joka siirtäisi ensimmäisen ottelun tulevaisuuteen.

## UpCloud (tai mikä tahansa Ubuntu-palvelin)

Valmiit tiedostot ovat kansiossa `deploy/`: `setup-server.sh` (kertaasennus), `em2028.service` (systemd), `Caddyfile` (HTTPS), `env.example` (asetukset), `backup.sh` (varmuuskopiot) ja `deploy.sh` (päivitys omalta koneelta yhdellä komennolla). Vaiheittaiset ohjeet on keskusteluvastauksessa, jossa tämä paketti toimitettiin; lyhyesti: luo palvelin, osoita domain siihen, aja `setup-server.sh`, kopioi tiedostot paikoilleen, täytä `/etc/em2028.env`, aja `deploy/deploy.sh`.

## Tuotantoon

### Docker + Caddy

```sh
docker build -t em2028 .
docker run -d --name em2028 --restart unless-stopped \
  -e APP_SECRET -e TRUST_PROXY=true -e BASE_URL=https://veikkaus.example.com \
  -e SMTP_HOST -e SMTP_USER -e SMTP_PASS -e MAIL_FROM \
  -v em2028-data:/data -p 127.0.0.1:8080:8080 \
  --read-only --cap-drop ALL --security-opt no-new-privileges em2028
```

Caddyfile (hoitaa HTTPS-varmenteen automaattisesti ja välittää Host-otsakkeen, jota CSRF-tarkistus käyttää):

```
veikkaus.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

### Suoraan palvelimelle (esim. UpCloud, systemd)

Käytä kiinteää järjestelmäkäyttäjää (ei `DynamicUser`), jotta komentorivin `admin`-komennot voi ajaa samalla käyttäjällä kuin palvelun:

```sh
sudo useradd --system --home /var/lib/em2028 em2028
sudo -u em2028 DB_PATH=/var/lib/em2028/em2028.db /opt/em2028/em2028 admin grant Matti all
```

Palvelun asetuksissa: `User=em2028`, `StateDirectory=em2028`, `EnvironmentFile=/etc/em2028.env` (oikeudet 600), `ADDR=127.0.0.1:8080`, `TRUST_PROXY=true`, `NoNewPrivileges=yes`, `ProtectSystem=strict`.

## Tietoturva ja tietosuoja

**Mitä tallennetaan:** nimimerkki, sähköposti (salattuna), salasanan argon2id-tiiviste, veikkaukset, chat-viestit, istuntojen ja kertakäyttölinkkien HMAC-tiivisteet, ylläpitäjien oikeudet ja ylläpitoloki.
**Mitä ei tallenneta:** selkokielinen sähköposti tai salasana, oikea nimi, IP-osoite, selaintiedot. Palvelinlokiin menee vain metodi, polku, tilakoodi ja kesto. Sähköpostiosoitteita ei kirjata lokiin (kehitystila `MAIL_MODE=log` on poikkeus).

| Uhka | Suojaus |
|---|---|
| Tietokanta vuotaa | Salasanat ovat argon2id-tiivisteitä (m=19 MiB, t=2) palvelimen salaisuudella höystettynä, joten pelkkä tietokanta ei riitä niiden murtamiseen. Sähköpostit ovat AES-256-GCM-salattuja ja haettavissa vain HMAC-hakemistolla. Istunnot ja kertakäyttölinkit ovat HMAC-tiivisteitä. |
| Salasanojen arvaus ja uudelleenkäyttö | Vähintään 10 merkkiä, yleisimmät salasanat ja nimimerkki/sähköposti estetty. Kirjautumisyritykset on rajoitettu sekä IP:tä (10/min) että osoitetta kohti (5 / 15 min). Tuntematon osoite ja väärä salasana antavat saman vastauksen ja saman vasteajan. |
| Tunnuksen kaappaus sähköpostilla | Palautuslinkki on 256-bittinen, kertakäyttöinen ja voimassa tunnin. Vastaus on aina sama riippumatta siitä, onko osoite rekisteröity. Salasanan vaihdosta lähtee ilmoitus ja kaikki istunnot mitätöidään. |
| Postin väärinkäyttö | Viestejä lähtee enintään 3 / tunti osoitetta kohti (palautus) ja 1 / min (vahvistus). SMTP vaatii TLS:n, otsakkeiden injektio estetty. |
| XSS | Tiukka CSP (`script-src 'self'`, ei inline-skriptejä), käyttöliittymä ei käytä `innerHTML`:ää. Istunto on `HttpOnly`-evästeessä. |
| CSRF | `SameSite=Strict`, pakollinen `X-Requested-With`-otsake ja Origin-tarkistus kaikissa muokkauksissa (myös ylläpidossa). |
| Istunnon varastaminen | `__Host-`-etuliite, `Secure`, 30 pv vanheneminen, uloskirjaus, salasanan vaihto ja palautus mitätöivät istunnot. |
| SQL-injektio | Kaikki kyselyt parametrisoituja. |
| Ylläpidon väärinkäyttö | Oikeudet vain palvelimella, oikeuskohtainen pääsy, tuore kirjautuminen, ylläpitolokista näkee kuka teki mitä, ylläpitäjiin ei voi kohdistaa moderointia. |
| Huijaus | Lukitus, kaavion laskenta ja pisteytys tehdään vain palvelimella: asiakkaan lähettämiin joukkueisiin ei luoteta. Muiden veikkaukset näkyvät vasta lukituksen jälkeen. |
| Chat | Viestit näytetään aina tekstinä (ei HTML:ää), ohjaus- ja näkymättömät merkit hylätään, linkit eivät ole klikattavia. 1 viesti / 3 s, uusi tunnus voi kirjoittaa vasta minuutin kuluttua, 5000 viimeisintä säilytetään. Moderaattori voi mykistää ja poistaa. |
| Väärinkäyttö | Nopeusrajoitus, 16 kt pyyntökoko, tuntemattomat JSON-kentät hylätään, aikakatkaisut. |
| Kontti | Distroless, ei shelliä, ei-root-käyttäjä, vain-luku-tiedostojärjestelmä. |

Pelaajan oikeudet: **Tili**-välilehdeltä voi vaihtaa salasanan tai poistaa tunnuksen ja kaikki tiedot pysyvästi (vaatii salasanan). Kopio tietosuojaselosteesta on tiedostossa `web/privacy.html`: täydennä siihen rekisterinpitäjän tiedot ennen julkaisua.

### Rajoitukset, jotka kannattaa tietää

- **Nyt tallennetaan henkilötietoja (sähköposti).** Se tuo velvoitteita: tietosuojaseloste, tietojen poisto pyynnöstä (Tili-välilehti hoitaa pelaajan puolesta), ja tietomurrosta ilmoittaminen tietosuojavaltuutetulle 72 tunnin kuluessa. Jos et halua velvoitteita, voit palata aiempaan avainmalliin.
- **Rekisteröinti paljastaa, onko sähköposti jo käytössä** (virheilmoitus "tällä sähköpostilla on jo tunnus"). Kirjautuminen ja salasanan palautus eivät paljasta sitä. Rekisteröintiä rajoitetaan IP-kohtaisesti.
- **Salasanojen vuotaneisiin listoihin ei verrata** (esim. Have I Been Pwned), jotta mitään ei lähetetä ulkopuolelle. Tarkistus on helppo lisätä.
- **Sähköpostin vaihto ei ole vielä mahdollista** Tili-välilehdeltä. Salasanan palautus toimii vain rekisteröidyllä osoitteella.
- **Ylläpitäjän tunnus on avain koko järjestelmään.** Käytä pitkää, ainutlaatuista salasanaa ja anna vain tarvittavat oikeudet.
- **Nimimerkit, pisteet ja chat-viestit näkyvät kaikille kirjautuneille pelaajille.** Muistuta pelaajia olemaan käyttämättä oikeaa nimeään ja olemaan kirjoittamatta henkilötietojaan chattiin.
- **Chat ei ole reaaliaikainen:** selain hakee uudet viestit 4 sekunnin välein. Se riittää satoihin yhtäaikaisiin käyttäjiin yhdellä palvelimella. Tuhansilla kävijöillä vaihda WebSocketiin tai SSE:hen.
- **Moderointi on käsityötä.** Nimimerkeille ei ole sanasuodatinta, joten seuraa chattiä ja käytä Käyttäjät- ja Chat-näkymiä.
- **HTTPS on pakollinen tuotannossa.** Ilman sitä eväste ja avain kulkisivat selkokielisinä.
- Varmuuskopioi tietokanta salattuna (SQLiten `.backup`-komennolla, ei suoraan tiedostoa kopioiden) ja pidä `APP_SECRET` erillään siitä.
- Koodia ei ole ulkopuolisen tarkastettu eikä tietoturvatestattu. Aja ennen julkaisua `go vet ./...`, `go test ./...` ja `govulncheck ./...`, ja harkitse ulkopuolista katselmusta, jos kävijöitä on paljon.
- Nopeusrajoitus on muistissa, eli yhden instanssin malli.

## Käyttöliittymän periaatteet (yksinkertainen kaikille)

- **Yksi asia kerrallaan:** Veikkaukset on kolme selkeää vaihetta (1 Lohkot, 2 Pudotuspelit, 3 Maalikuningas). Jokaisessa näkyy edistyminen (esim. 12/36) ja valmiista vaiheesta ✓.
- **Tulokset heti näkyvissä:** ottelut ovat ensimmäisellä ruudulla, lohkotaulukko niiden alla (isolla näytöllä vierellä).
- **Nopea syöttö:** numeron kirjoittamisen jälkeen kohdistin siirtyy itsestään seuraavaan kenttään. Tallennus tapahtuu automaattisesti.
- **"Seuraava"-painikkeet** vievät lohkosta toiseen ja lopuksi pudotuspeleihin ja maalikuninkaaseen.
- **Pudotuspelit avautuvat, kun lohkot on veikattu:** ennen sitä sivu kertoo selkeästi, mitä puuttuu.
- **Selkokieliset termit:** taulukossa Pelit, Maalit ja Pisteet. Tulostaulussa vain pisteet, ja lisätiedot näkyvät pelaajan omalla sivulla.
- **Ensikertalaisen opas** (yksi rivi, piilotettavissa) ja **tiivis tilarivi**: milloin veikkaukset sulkeutuvat ja kuinka paljon on tehty.
- Isompi kirjasin (17 px), isot kosketuskohteet ja neljä välilehteä, jotka mahtuvat puhelimen leveyteen.

## Ominaisuudet ja ylläpidon lisät

**Pelaajalle**
- **Kaaviopuu** (Veikkaukset → Kaavio): pudotuspelit puuna, oikeat tulokset ja pisteet näkyvät lukituksen jälkeen.
- **Toisen pelaajan veikkaukset:** lukituksen jälkeen tulostaulun nimestä aukeaa pelaajan koko kaavio, tulokset ja pisteet.
- **Sijoituksen muutos:** tulostaulussa ▲/▼ verrattuna päivän alun tilanteeseen (tallennetaan päivittäin).
- **Sähköpostimuistutus:** 24 h ja 1 h ennen lukitusta, vain jos veikkaukset ovat kesken. Peruutus yhdellä klikkauksella (`/unsubscribe.html`) tai Tili-välilehdeltä.
- **Tietojen lataus** (Tili → Lataa tietoni) ja **tilin poisto**. Jaa veikkaus -painike mestarin ilmoittamiseen.
- **Asennettava sovellus (PWA):** `manifest.json` ja kuvakkeet, jolloin sivun voi lisätä puhelimen kotinäytölle.
- **Chatin ilmiannot:** kolme ilmiantoa piilottaa viestin muilta, kunnes ylläpito käsittelee sen.

**Ylläpitäjälle** (`/admin.html`)
- **Kaksivaiheinen kirjautuminen (TOTP)** Authenticator-sovelluksella: Ylläpitäjät ja loki -näkymästä käyttöön. Puhelimen kadotessa: `em2028 admin totp-reset <nimimerkki>`.
- **Lohkon järjestyksen käsin asetus** (Ottelut → Lohkot) ja **neljän parhaan kolmosen käsin valinta** (Kaavio), kun tasatilanne ulottuu UEFAn kriteereihin, joita palvelin ei tiedä.
- **Tulosten tuonti** (Ottelut → Tuo tulokset): liitä rivit muodossa `12 2-1` tai `40 1-1 away`. Jokainen rivi tarkistetaan samoilla säännöillä kuin käsin syötetty tulos.
- **Chatin moderointi:** ilmiantojen määrä näkyy, kieltolista (sanat, joita sisältävät viestit hylätään), mykistys ja poisto.
- **Ilmoitukset:** webhook (`NOTIFY_WEBHOOK_URL`) kertoo uudesta tuloksesta.

**Palvelimelle**
- **Terveystarkistus:** `GET /healthz` palauttaa `ok`, kun tietokanta vastaa. Käytä sitä ulkopuolisessa seurannassa.
- **Versioidut tietokantamigraatiot** (`PRAGMA user_version`): päivitys vanhaan tietokantaan on turvallinen. Lisää uusi muutos `migrations`-listan loppuun tiedostossa `db.go`.
- **Tulostaulun välimuisti** (15 s, tyhjenee aina ylläpidon muutoksesta): kestää tuhansia pelaajia.
- **Jatkuva varmuuskopiointi:** katso `deploy/LITESTREAM.md`.
- **Kisojen jälkeen:** `CONFIRM_PURGE=yes em2028 admin purge-players` poistaa kaikki pelaajatiedot (ylläpitäjät säilyvät). Ota ensin varmuuskopio.

## Testaus ja laadunvarmistus

```sh
go vet ./... && go test ./...        # yksikkö- ja integraatiotestit (pisteytys, kaavio, tili, 2FA, muistutukset, ylläpito)
python e2e/ui/ui_tests.py            # selainkäyttöliittymä oikealla selaimella testipalvelinta vasten (ei vaadi Go:ta)
python e2e/run_e2e.py                # koko polku OIKEALLA palvelimella: rekisteröinti, vahvistus, veikkaus, lukitus, tulostaulu, ylläpito
```

Python-testit vaativat `pip install playwright && playwright install chromium`. GitHub Actions (`.github/workflows/ci.yml`)
ajaa kaiken (myös `govulncheck`) jokaisella muutoksella. **Huom:** `go test` ja `run_e2e.py` on kirjoitettu ilman Go-kääntäjää
eikä niitä ole ajettu: aja ne ensimmäiseksi ja korjaa mahdolliset käännösvirheet.

## Rakenne

```
main.go        konfiguraatio ja käynnistys (+ "admin"-komento)
server.go      reitit, väliohjelmistot (CSP, CSRF, nopeusrajoitus, lokitus)
auth.go        istunnot, syötteiden tarkistus
account.go     rekisteröinti, kirjautuminen, vahvistus, salasanan palautus
password.go    argon2id, salasanasäännöt, sähköpostin salaus
mail.go        SMTP (TLS pakollinen) ja viestipohjat
handlers.go    API: tili, ottelut, veikkaukset, tulostaulu, chat
admin.go       ylläpito-API: oikeudet, ottelut, joukkueet, käyttäjät, chat, loki
cli.go         em2028 admin list|grant|revoke
dev.go         em2028 dev simulate|clear (vain testaukseen)
board.go       tulostaulu: välimuisti, sijoituksen muutos, päivän tilannekuvat
reminders.go   muistutukset, peruutuslinkki, webhook-ilmoitukset, asetukset
totp.go        ylläpitäjien kaksivaiheinen kirjautuminen
admin_extra.go lohkojen järjestyksen ja parhaiden kolmosten käsin asetus
chat_extra.go  chatin ilmiannot ja kieltolista
export.go      pelaajan tietojen lataus
pwned.go       vuotaneiden salasanojen tarkistus (valinnainen)
deploy/        palvelimen asennus, systemd, Caddy, varmuuskopio, Litestream ja päivitysskripti
e2e/           selainkäyttöliittymätestit (ui/) ja täysi E2E (run_e2e.py)
bracket.go     lohkotaulukot, tasapistesäännöt ja kolmossijataulukko
scoring.go     pisteytys
predictions.go lukitus, oma kaavio, jatkopisteet, tulostaulu
db.go          SQLite-skeema ja otteluohjelma (51 ottelua)
*_test.go      pisteytys, kaavio, chat, tili- ja salasanavirrat, ylläpidon pääsynhallinta
web/           käyttöliittymä (embed): index.html + app.js, admin.html + admin.js, verify/reset/privacy.html, bling.js (konfetti), style.css
```
