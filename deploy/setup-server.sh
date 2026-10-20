#!/usr/bin/env bash
# Ajetaan kerran uudella Ubuntu-palvelimella rootina: sudo bash setup-server.sh
set -euo pipefail

apt-get update
apt-get install -y sqlite3 ufw unattended-upgrades curl gpg debian-keyring debian-archive-keyring apt-transport-https

# Caddy (HTTPS-varmenteet automaattisesti). Tarkista tarvittaessa ajantasaiset ohjeet: caddyserver.com/docs/install
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' > /etc/apt/sources.list.d/caddy-stable.list
apt-get update
apt-get install -y caddy

# Oma järjestelmäkäyttäjä palvelulle (ei kirjautumista)
id em2028 >/dev/null 2>&1 || useradd --system --home /var/lib/em2028 --shell /usr/sbin/nologin em2028
mkdir -p /opt/em2028 /var/lib/em2028 /var/backups/em2028
chown em2028:em2028 /var/lib/em2028
chmod 700 /var/lib/em2028 /var/backups/em2028

# Palomuuri: vain SSH, HTTP ja HTTPS
ufw allow OpenSSH
ufw allow 80/tcp
ufw allow 443/tcp
ufw --force enable

# Automaattiset tietoturvapäivitykset
dpkg-reconfigure -f noninteractive unattended-upgrades || true

echo "Valmis. Seuraavaksi: kopioi deploy/em2028.service, deploy/Caddyfile, deploy/backup.sh ja luo /etc/em2028.env (ks. README)."
