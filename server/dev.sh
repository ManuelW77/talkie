#!/bin/sh
# Lokaler Testbetrieb: Talkie-Server mit Verwaltungsseite + Mailpit.
#   ./dev.sh up | down | logs | mails
set -eu
cd "$(dirname "$0")"
dc() { docker compose -f docker-compose.yml -f docker-compose.dev.yml "$@"; }
case "${1:-up}" in
  up)
    dc up -d --build
    echo
    echo "Verwaltung: http://localhost:8090  (Anmelden mit admin@talkie.local)"
    echo "Mailpit:    http://localhost:8026  (Login-Codes und Einladungen landen hier)"
    ;;
  down) dc down ;;
  logs) dc logs -f talkie ;;
  mails) open http://localhost:8026 2>/dev/null || xdg-open http://localhost:8026 ;;
  *) echo "Benutzung: ./dev.sh up|down|logs|mails"; exit 1 ;;
esac
