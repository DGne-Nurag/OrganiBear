#!/usr/bin/env bash
# Schreibt die Lizenztexte aller eingebauten Go-Module (alle Plattformen,
# mit Programmfenster) nach stdout. Kommt als THIRD_PARTY_NOTICES.txt ins Release.
set -euo pipefail
echo "OrganiBear enthält folgende Fremdsoftware:"
for os in windows darwin linux; do
  GOOS=$os CGO_ENABLED=1 go list -deps -tags desktop,production,webkit2_41 \
    -f '{{with .Module}}{{if not .Main}}{{.Path}} {{.Version}} {{.Dir}}{{end}}{{end}}' .
done | sort -u | while read -r path ver dir; do
  lic=$(find "$dir" -maxdepth 1 -iname 'licen[cs]e*' -o -maxdepth 1 -iname 'copying*' | sort | head -1)
  [ -n "$lic" ] || { echo "Keine Lizenzdatei für $path" >&2; exit 1; }
  printf '\n\n==== %s %s ====\n\n' "$path" "$ver"
  cat "$lic"
  notice=$(find "$dir" -maxdepth 1 -iname 'notice*' | head -1)
  if [ -n "$notice" ]; then printf '\n'; cat "$notice"; fi
done
