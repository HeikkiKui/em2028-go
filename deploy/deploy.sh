#!/usr/bin/env bash
# Ajetaan OMALTA koneelta projektin kansiosta:  deploy/deploy.sh käyttäjä@palvelin
# Testaa, rakentaa Linux-ohjelman, ottaa varmuuskopion, korvaa ohjelman ja käynnistää palvelun uudelleen.
set -euo pipefail
HOST="${1:?Käyttö: deploy/deploy.sh käyttäjä@palvelin}"
cd "$(dirname "$0")/.."

go vet ./...
go test ./...
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o em2028.build .
scp em2028.build "$HOST":/tmp/em2028.new
rm em2028.build

ssh "$HOST" '
  set -e
  sudo /usr/local/bin/em2028-backup || true
  sudo cp /opt/em2028/em2028 /opt/em2028/em2028.prev 2>/dev/null || true
  sudo install -o root -g root -m 755 /tmp/em2028.new /opt/em2028/em2028
  rm /tmp/em2028.new
  sudo systemctl restart em2028
  sleep 2
  systemctl is-active em2028
'
echo "Valmis. Palautus tarvittaessa: ssh $HOST 'sudo cp /opt/em2028/em2028.prev /opt/em2028/em2028 && sudo systemctl restart em2028'"
