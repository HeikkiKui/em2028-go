#!/usr/bin/env bash
# Asenna: sudo install -m 755 backup.sh /usr/local/bin/em2028-backup
# Ajastus: echo '17 3 * * * root /usr/local/bin/em2028-backup' | sudo tee /etc/cron.d/em2028-backup
# Käyttää SQLiten omaa .backup-komentoa (turvallinen, vaikka palvelu on käynnissä).
set -euo pipefail
DB=/var/lib/em2028/em2028.db
DIR=/var/backups/em2028
mkdir -p "$DIR"
OUT="$DIR/em2028-$(date +%F-%H%M).db"
sqlite3 "$DB" ".backup '$OUT'"
chmod 600 "$OUT"
find "$DIR" -name 'em2028-*.db' -mtime +14 -delete
echo "Varmuuskopio: $OUT"
