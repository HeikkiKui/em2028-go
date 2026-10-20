# Jatkuva varmuuskopiointi (Litestream)

Yöllinen `backup.sh` ottaa täyden kopion kerran vuorokaudessa. Litestream replikoi tietokannan muutokset
jatkuvasti ulkoiseen tallennustilaan, joten pahimmillaankin menetät vain muutaman sekunnin tiedot.
Tämä on valinnainen lisä. En ole testannut näitä ohjeita oikealla palvelimella: tarkista ajantasaiset
asennusohjeet osoitteesta litestream.io.

1. **Tallennustila:** luo S3-yhteensopiva bucket (esim. UpCloud Object Storage) ja erilliset käyttöoikeustunnukset
   vain tälle bucketille. Salaus kannattaa ottaa käyttöön bucketin asetuksista.
2. **Asennus:** lataa julkaisu litestream.io-sivun ohjeen mukaan (Debian/Ubuntu-paketti `.deb`) ja asenna se `dpkg -i`:llä.
3. **Asetus:** kopioi `deploy/litestream.yml` palvelimelle tiedostoon `/etc/litestream.yml` ja aseta ympäristömuuttujat
   (`LITESTREAM_BUCKET`, `LITESTREAM_ENDPOINT`, `LITESTREAM_ACCESS_KEY_ID`, `LITESTREAM_SECRET_ACCESS_KEY`)
   tiedostoon `/etc/default/litestream` tai systemd-palvelun `EnvironmentFile`-asetukseen. Tiedoston oikeudet: `chmod 600`.
4. **Käynnistys:** `sudo systemctl enable --now litestream` ja tarkista `journalctl -u litestream -n 20`.
5. **Palautus:** pysäytä sovellus (`sudo systemctl stop em2028`), siirrä vanha tietokanta sivuun ja aja
   `sudo -u em2028 litestream restore -config /etc/litestream.yml -o /var/lib/em2028/em2028.db /var/lib/em2028/em2028.db`.
   Käynnistä sen jälkeen sovellus. **Kokeile palautus testipalvelimella ennen kuin tarvitset sitä.**
6. **Muista:** varmuuskopio sisältää salattuja sähköposteja ja salasanatiivisteitä. Ilman `APP_SECRET`-arvoa niitä ei voi
   käyttää, joten säilytä `APP_SECRET` erikseen ja turvallisesti.

## Seuranta
Sovelluksessa on terveystarkistus osoitteessa `/healthz` (palauttaa `ok`, kun tietokanta vastaa). Lisää se
ulkopuoliseen seurantapalveluun (esim. UptimeRobot, Better Stack tai Healthchecks.io) ja aseta hälytys sähköpostiin.
