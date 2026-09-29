# ʕ•ᴥ•ʔ OrganiBear

Der Bär, der deine Filme und Serien aufräumt.

OrganiBear nimmt einen Ordner voller wild benannter Videodateien
(`The.Matrix.1999.1080p.BluRay.x264-GRP.mkv`, `breaking bad - 1x03.avi` …),
erkennt Titel, Jahr, Staffel und Folge, holt die richtigen Daten von
[TMDB](https://www.themoviedb.org) und sortiert alles nach deiner eigenen Vorlage
in eine ordentliche Bibliothek.

![Screenshot](docs/screenshot.png)

## Was er kann

- **Ein einziges Programm.** Starten, der Browser öffnet sich, fertig. Keine Installation, keine Abhängigkeiten.
- **Versteht viele Namensmuster:** `S01E02`, `1x02`, `Staffel 1 Folge 2`, Doppelfolgen (`S02E01E02`), Release-Gruppen, Qualitätsangaben, Jahreszahlen im Titel (`Blade Runner 2049 (2017)`), Ordnernamen wie `Serie/Staffel 2/05.mkv`.
- **Metadaten von TMDB:** richtiger Titel in deiner Sprache, Jahr und Folgentitel. Bei Unsicherheit wählst du aus den Treffern, suchst selbst oder trägst die Daten von Hand ein. Ohne API-Key arbeitet er nur mit den Dateinamen.
- **Eigene Namensvorlagen** für Filme und Serien, mit Live-Vorschau.
- **Dateiregeln:** Du legst fest, welche Dateiendungen Videos und welche Begleitdateien sind (Untertitel, NFO, Bilder …) und ob sie verschoben, kopiert oder ignoriert werden. Begleitdateien bekommen den neuen Namen ihres Videos (`Film (2010).de.srt`).
- **Erst Vorschau, dann Aktion.** Nichts wird bewegt, bevor du bestätigst. Vorhandene Dateien werden nie überschrieben.
- **Rückgängig:** Jeder Lauf landet im Verlauf und lässt sich mit einem Klick zurückdrehen.

## Loslegen

1. Programm für dein System bauen (siehe unten) oder aus den CI-Artefakten laden.
2. Starten. Das Webinterface öffnet sich im Browser. Der Link steht auch im Programmfenster.
3. Unter **Einstellungen** den TMDB-Key eintragen (kostenlos unter
   [themoviedb.org/settings/api](https://www.themoviedb.org/settings/api)), Vorlagen und Dateiregeln anpassen.
4. Unter **Sortieren** Quelle und Ziel wählen, **Schnüffeln** drücken, Vorschau prüfen, **Einsortieren**.

### Optionen

```
organibear [-addr 127.0.0.1:8765] [-config pfad/organibear.json] [-no-browser]
```

Die Konfiguration liegt standardmäßig als `organibear.json` neben dem Programm,
der Verlauf im Ordner `organibear-verlauf` daneben.

## Sicherheit und Barrierefreiheit

- Das Webinterface lauscht nur auf dem eigenen Rechner (127.0.0.1) und lässt sich nicht ins Netzwerk öffnen.
- Zugang gibt es nur über den Startlink mit einem zufälligen Schlüssel, der bei jedem Start neu erzeugt wird. Andere Programme, andere Benutzer und fremde Webseiten im Browser kommen nicht an die Oberfläche.
- Dateien werden nie überschrieben, auch nicht bei gleichzeitigen Schreibzugriffen, sofern das Dateisystem Hardlinks kann (NTFS, APFS, ext4 …). Symlinks werden weder als Quelle verfolgt noch als Ausweg aus dem Zielordner zugelassen.
- Konfiguration (mit API-Key) und Verlauf sind unter macOS und Linux nur für den eigenen Benutzer lesbar. Der Verlauf wird vor jedem Rückgängigmachen geprüft.
- Die Oberfläche ist mit Tastatur und Screenreader bedienbar, erfüllt die Kontrastvorgaben nach WCAG 2.2 AA in hellem und dunklem Modus und respektiert „Bewegung reduzieren“.

## Vorlagen

`/` trennt Ordner, die Dateiendung wird automatisch angehängt. Zahlen lassen sich
mit `:breite` auffüllen (`{season:02}` → `01`). Leere Platzhalter verschwinden
samt Klammern und Strichen.

| Platzhalter | Bedeutung |
|---|---|
| `{title}` | Titel (in der eingestellten Sprache) |
| `{original_title}` | Originaltitel |
| `{year}` | Erscheinungsjahr |
| `{season}`, `{episode}` | Staffel, Folge (Doppelfolgen werden zu `01-E02`) |
| `{episode_title}` | Folgentitel |
| `{resolution}` | Auflösung aus dem Dateinamen, z. B. `1080p` |
| `{tmdb_id}` | TMDB-ID |
| `{first_letter}` | Anfangsbuchstabe ohne Artikel, z. B. `M` für „The Matrix“ |

Standard:

```
Filme/{title} ({year})/{title} ({year})
Serien/{title} ({year})/Staffel {season:02}/{title} - S{season:02}E{episode:02} - {episode_title}
```

## Selbst bauen

Benötigt [Go](https://go.dev) 1.24 oder neuer.

```sh
go build -o organibear .
# für Windows:
GOOS=windows GOARCH=amd64 go build -o organibear.exe .
```

Tests: `go test ./...`

CI prüft bei jedem Push und zusätzlich jeden Montag:

- `go test` inklusive Sicherheitstests (`security_test.go`)
- `govulncheck`: bekannte Sicherheitslücken in Go und im Code
- `staticcheck` und `gosec`: statische Analyse (bewusste Ausnahmen sind mit `#nosec` und Begründung markiert)
- Barrierefreiheit nach WCAG 2.2 AA mit Playwright und axe, hell und dunkel, inklusive Tastaturbedienung und 320 px Breite:
  `cd e2e && npm ci && npx playwright install chromium && npm test`

## TMDB

<a href="https://www.themoviedb.org"><img src="web/tmdb.svg" alt="The Movie Database (TMDB)" height="16"></a>

This product uses the TMDB API but is not endorsed or certified by TMDB.
Dieses Produkt nutzt die TMDB-API, wird aber von TMDB weder unterstützt noch zertifiziert.

Der Hinweis und das Logo stehen auch im Programm unten im Bereich „Über OrganiBear“.
Die kostenlose TMDB-API ist nur für nicht-kommerzielle Nutzung gedacht; jeder Nutzer trägt seinen eigenen API-Key ein.
