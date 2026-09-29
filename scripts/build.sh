#!/usr/bin/env bash
# Baut ein OrganiBear-Programm. Genutzt von CI und Release.
#
#   VERSION=v0.2.0 GOOS=windows GOARCH=amd64 KIND=desktop scripts/build.sh dist/organibear.exe
#
# KIND=desktop: eigenes Programmfenster (Wails). Windows geht ohne cgo,
#               macOS und Linux brauchen cgo und müssen auf dem Zielsystem bauen,
#               Linux zusätzlich libgtk-3-dev und libwebkit2gtk-4.1-dev.
# KIND=browser: nur Browser-Oberfläche, ohne cgo, läuft überall (NAS, Server).
# OS_KEY, TVDB_KEY: App-Keys aus den Secrets, stehen nie im Repo.
set -euo pipefail
out=$1
ldflags="-s -w -X main.version=${VERSION:-dev} -X main.openSubtitlesKey=${OS_KEY:-} -X main.tvdbKey=${TVDB_KEY:-}"
tags=""
export CGO_ENABLED=0
if [ "${KIND:-browser}" = desktop ]; then
  tags="desktop,production"
  case "$GOOS" in
    windows) ldflags="$ldflags -H windowsgui" ;;
    darwin)
      export CGO_ENABLED=1
      export CGO_CFLAGS="-mmacosx-version-min=11.0"
      export CGO_LDFLAGS="-framework UniformTypeIdentifiers -mmacosx-version-min=11.0"
      if [ "$GOARCH" = amd64 ]; then export CC="clang -arch x86_64"; fi
      ;;
    linux)
      export CGO_ENABLED=1
      tags="$tags,webkit2_41"
      ;;
  esac
fi
go build -trimpath -tags "$tags" -ldflags "$ldflags" -o "$out" .
