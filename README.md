# ʕ•ᴥ•ʔ OrganiBear

Der Bär, der deine Filme und Serien aufräumt.

OrganiBear nimmt einen Ordner voller wild benannter Videodateien
(`The.Matrix.1999.1080p.BluRay.x264-GRP.mkv`, `breaking bad - 1x03.avi` …),
erkennt Titel, Jahr, Staffel und Folge, holt die richtigen Daten von
[TMDB](https://www.themoviedb.org) und sortiert alles nach deiner eigenen Vorlage
in eine ordentliche Bibliothek.

![Screenshot](docs/screenshot.png)

## Was er kann

- **Ein einziges Programm.** Starten, das Fenster geht auf, fertig. Keine Installation.
- **Ordner einfach hineinziehen:** Quelle und Ziel per Drag & Drop aus dem Explorer, Finder oder Dateimanager.
- **Versteht viele Namensmuster:** `S01E02`, `1x02`, `Staffel 1 Folge 2`, Doppelfolgen (`S02E01E02`), Release-Gruppen, Qualitätsangaben, Jahreszahlen im Titel (`Blade Runner 2049 (2017)`), Ordnernamen wie `Serie/Staffel 2/05.mkv`, mehrteilige Filme (`Titanic.1997.CD1.avi` wird zu `Titanic (1997) - part1.avi`) und fortlaufende Folgennummern wie bei Anime (`One.Piece.E1071.mkv` wird über TMDB in Staffel und Folge umgerechnet).
- **Metadaten von TMDB:** richtiger Titel in deiner Sprache, Jahr und Folgentitel. Bei Unsicherheit wählst du aus den Treffern, suchst selbst oder trägst die Daten von Hand ein. Bei Serien wählst du Staffel und Folge aus der TMDB-Liste, der Folgentitel kommt dann mit. Ohne API-Key arbeitet er nur mit den Dateinamen.
- **IMDb- und TMDB-IDs:** Steht eine ID im Namen (`Matrix (1999) {imdb-tt0133093}`, `[tmdbid=603]`) oder in einer vorhandenen NFO, nimmt er genau diesen Titel. Im Suchfeld unter „Anpassen“ geht auch eine IMDb-ID wie `tt0133093`.
- **TheTVDB als zweite Quelle** für Serien, die TMDB nicht kennt, mit wahlweise DVD-Reihenfolge. Die Vorschau zeigt bei jedem Treffer, woher die Daten stammen.
- **Eigene Namensvorlagen** für Filme und Serien, mit Live-Vorschau.
- **Dateiregeln:** Du legst fest, welche Dateiendungen Videos und welche Begleitdateien sind (Untertitel, NFO, Bilder …) und ob sie verschoben, kopiert oder ignoriert werden. Begleitdateien bekommen den neuen Namen ihres Videos (`Film (2010).de.srt`).
- **Erst Vorschau, dann Aktion.** Nichts wird bewegt, bevor du bestätigst. Vorhandene Dateien werden nie überschrieben.
- **Liest die Datei selbst:** Auflösung, Video-Codec, HDR und Tonspuren kommen bei MKV und MP4 direkt aus der Datei,
  nicht nur aus dem Namen. Liegen zwei Versionen desselben Films vor, schlägt er die bessere vor
  („2160p HEVC statt 1080p H.264“) und wählt sie gleich aus.
- **Rückgängig:** Jeder Lauf landet im Verlauf und lässt sich mit einem Klick zurückdrehen.
- **Fehlende Untertitel** in deinen Sprachen lädt er auf Wunsch von OpenSubtitles.com nach.
- **Für Plex, Jellyfin und Kodi:** auf Wunsch NFO-Dateien, Poster und Hintergrundbilder dazu, danach liest der Mediaserver seine Bibliothek neu ein.

## Loslegen

1. Programm für dein System unter [Releases](https://github.com/DGne-Nurag/OrganiBear/releases) laden
   (oder selbst bauen, siehe unten). Unter macOS und Linux vorher `chmod +x` ausführen; macOS
   fragt beim ersten Start nach, weil das Programm nicht signiert ist (Rechtsklick › Öffnen).
   - Windows: `organibear-…-windows-amd64.exe`. Das Fenster nutzt die in Windows 10 und 11 eingebaute
     WebView2 (Microsoft Edge), die dort normalerweise schon installiert ist.
   - macOS: `organibear-…-macos-arm64` (Apple Silicon) oder `-macos-amd64` (Intel), ab macOS 11.
   - Linux mit Desktop: `organibear-…-linux-amd64-desktop` bzw. `-arm64-desktop`. Braucht GTK 3 und
     WebKitGTK 4.1 (Debian/Ubuntu: `sudo apt install libgtk-3-0 libwebkit2gtk-4.1-0`, meist schon da).
   - Linux ohne Desktop (Server, NAS, Raspberry Pi): `organibear-…-linux-amd64` bzw. `-arm64`, nur mit Browser.
2. Starten. OrganiBear öffnet sein eigenes Fenster. Klappt das nicht (oder bei den Linux-Versionen ohne
   Desktop), öffnet sich die Oberfläche stattdessen im Browser; der Link steht dann auch im Terminal.
3. Beim ersten Start führt der Bär Schritt für Schritt zum kostenlosen TMDB-Key: Konto anlegen, Key beantragen
   (mit Vorlage zum Kopieren für das Formular), einfügen, fertig. Der Key wird vor dem Speichern bei TMDB geprüft.
   Später findest du die Anleitung unter **Einstellungen**, dort auch Vorlagen und Dateiregeln.
4. Unter **Sortieren** wählen, was passieren soll:
   - **Einsortieren:** Quelle und Ziel wählen, die Dateien kommen in die Bibliothek im Zielordner.
   - **Nur umbenennen:** nur die Quelle wählen. Die Dateien bleiben dort und bekommen Namen und Unterordner
     nach deinen Vorlagen; leere alte Ordner werden weggeräumt, Kopieren wird dabei zu Umbenennen.
5. **Schnüffeln** drücken, Vorschau prüfen, **Einsortieren** bzw. **Umbenennen**. Alles lässt sich im **Verlauf** rückgängig machen.

### Optionen

```
organibear [-browser] [-addr 127.0.0.1:8765] [-config pfad/organibear.json] [-no-browser] [-idle 5m]
```

`-browser` öffnet die Oberfläche im Browser statt im eigenen Fenster, `-no-browser` startet nur den Server
(die Adresse steht im Terminal). Drag & Drop von Ordnern geht nur im eigenen Fenster, weil Browser Webseiten
den Pfad eines abgelegten Ordners nicht verraten; dort bleibt der 📁-Knopf.

**Beenden:** Fenster schließen oder den Knopf „Beenden“ oben rechts. Im Browser-Modus geht auch Strg+C im
Terminal oder einfach Tab schließen: Ist 5 Minuten lang kein Tab mehr offen, legt sich der Bär von selbst schlafen
(`-idle 0` schaltet das ab). In allen drei Fällen wird ein laufendes Einsortieren vorher noch fertig;
ein zweites Strg+C bricht sofort ab.

Die Konfiguration liegt standardmäßig als `organibear.json` neben dem Programm,
der Verlauf im Ordner `organibear-verlauf` daneben.

## Mediaserver

Unter **Einstellungen › Mediaserver** lässt sich einschalten, was beim Einsortieren zusätzlich passiert
(nur für Einträge mit TMDB-Treffer):

- **NFO-Dateien** im Kodi-Format mit Titel, Inhalt, Genres sowie TMDB-, IMDb- und TheTVDB-ID. Kodi, Jellyfin und Emby
  lesen sie direkt, Plex mit einem NFO-Agent.
- **Bilder von TMDB:** `poster.jpg` und `fanart.jpg` im Film- bzw. Serienordner, Staffelposter (`season01-poster.jpg`)
  und Vorschaubilder für Folgen (`<Folge>-thumb.jpg`). Liegen mehrere Filme in einem Ordner, heißen die Bilder
  `<Film>-poster.jpg` und `<Film>-fanart.jpg`.
- **Bibliothek neu einlesen:** Nach dem Einsortieren stößt OrganiBear Plex (Adresse und Plex-Token), Jellyfin
  (Adresse und API-Schlüssel) und Kodi (Adresse der Web-Steuerung, ggf. Benutzer und Passwort) an. Mit „Testen“
  prüfst du die Zugangsdaten sofort.

Vorhandene Dateien werden nie überschrieben, „Rückgängig“ entfernt die angelegten Dateien wieder.
Die Zugangsdaten liegen nur in der lokalen Konfigurationsdatei.

## Untertitel

Unter **Einstellungen › Untertitel von OpenSubtitles** lassen sich fehlende Untertitel nachladen. Nach dem
Einsortieren prüft OrganiBear für jede eingestellte Sprache (Standard: `de, en`), ob schon ein Untertitel neben dem
Video liegt (auch `ger`, `deu`, `eng` usw. im Namen). Fehlt einer, sucht er per Datei-Fingerabdruck (OpenSubtitles-Hash)
und TMDB-ID und speichert den besten Treffer als `<Video>.de.srt`. Treffer mit passendem Fingerabdruck und
menschliche Übersetzungen gehen vor.

- Ohne Konto erlaubt OpenSubtitles 5 Downloads pro Tag, mit einem kostenlosen
  [OpenSubtitles-Konto](https://www.opensubtitles.com) 20, mit VIP bis zu 1000. Benutzer und Passwort trägst du
  optional in den Einstellungen ein. Ist das Tageslimit erreicht, sagt der Bär Bescheid.
- Einen eigenen API-Key trägst du nicht ein: OpenSubtitles erlaubt nur einen Key pro Anwendung, und den bringen die
  offiziellen Releases mit. Wer selbst baut, bekommt die Untertitel-Funktion nur mit einem eigenen
  [Consumer-Key](https://www.opensubtitles.com/consumers) (siehe „Selbst bauen“).
- Übertragen werden Fingerabdruck, Dateigröße, Sprachen und TMDB-ID, aber keine Dateinamen oder Pfade.
- Vorhandene Untertitel werden nie überschrieben, „Rückgängig“ entfernt die geladenen wieder.
- Laut den Nutzungsbedingungen von OpenSubtitles ist eine **kommerzielle Nutzung nicht erlaubt**.

## IMDb-IDs und TheTVDB

**IMDb-IDs** (`tt` und 7 bis 9 Ziffern) erkennt der Bär im Datei- oder Ordnernamen, in einer NFO neben dem Video
(`<Video>.nfo`, `movie.nfo`, bei Serien `tvshow.nfo`) und im Suchfeld. Aufgelöst werden sie über die
`/find`-Schnittstelle von TMDB. IMDb selbst wird nie abgefragt, denn IMDb erlaubt weder das Auslesen der Webseite
noch bietet es eine freie Schnittstelle. Eine IMDb-ID einer einzelnen Folge liefert Serie, Staffel und Folge.

**TheTVDB** schaltest du unter **Einstellungen › TheTVDB als zweite Quelle** ein. Findet TMDB zu einer Serie nichts,
sucht der Bär dort weiter, holt Folgentitel und rechnet fortlaufende Folgennummern um. Als Folgenreihenfolge gibt es
„wie ausgestrahlt“ und „wie auf DVD/Blu-ray“. Kennt TMDB dieselbe Serie über ihre TheTVDB-ID, bekommt der Eintrag
auch die TMDB-ID, und NFO-Dateien und Bilder klappen wie gewohnt. Treffer von TheTVDB tragen in der Vorschau den
Hinweis „Daten: TheTVDB“ mit einem Link auf die Serie bei TheTVDB.com.

Den TheTVDB-Key bringen die offiziellen Releases mit (Lizenzmodell „Negotiated Contract“: kostenlos für
Projekte mit weniger als 50.000 US-Dollar Umsatz im Jahr, mit Namensnennung und Link auf TheTVDB.com). Selbst gebaute
Programme ohne Key blenden die Einstellung aus. Neue Platzhalter: `{imdb_id}` und `{tvdb_id}`.

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
| `{resolution}` | Auflösung aus der Datei (sonst aus dem Namen), z. B. `2160p` |
| `{vcodec}` | Video-Codec aus der Datei, z. B. `HEVC`, `H.264`, `AV1` |
| `{hdr}` | `DV` (Dolby Vision), `HDR10` oder `HLG`, bei SDR leer |
| `{audio}` | Haupttonspur, z. B. `TrueHD 7.1` oder `EAC3 5.1` |
| `{languages}` | Sprachen der Tonspuren, z. B. `DE-EN` |
| `{part}` | Teil eines mehrteiligen Films, z. B. `part1`. Fehlt er in der Vorlage, hängt der Bär ` - part1` an den Dateinamen |
| `{tmdb_id}` | TMDB-ID |
| `{imdb_id}` | IMDb-ID, z. B. `tt0133093` |
| `{tvdb_id}` | TheTVDB-ID |
| `{first_letter}` | Anfangsbuchstabe ohne Artikel, z. B. `M` für „The Matrix“ |

Standard:

```
Filme/{title} ({year})/{title} ({year})
Serien/{title} ({year})/Staffel {season:02}/{title} - S{season:02}E{episode:02} - {episode_title}
```

## Selbst bauen

Benötigt [Go](https://go.dev) 1.26 oder neuer.

```sh
go build -o organibear .              # nur Browser, läuft überall
scripts/build.sh organibear           # dasselbe über das Build-Skript
GOOS=windows GOARCH=amd64 KIND=desktop scripts/build.sh organibear.exe   # mit Fenster
```

Mit eigenem Fenster (`KIND=desktop`, Bibliothek [Wails](https://wails.io)): Windows lässt sich von überall bauen.
macOS und Linux brauchen cgo und müssen auf dem Zielsystem gebaut werden, Linux zusätzlich
`libgtk-3-dev` und `libwebkit2gtk-4.1-dev`.

Tests: `go test ./...`

Selbst gebaute Programme enthalten keinen OpenSubtitles-Key, die Untertitel-Funktion ist dann ausgeblendet.
Wer eine eigene Version verteilt, legt bei OpenSubtitles einen eigenen Consumer an und baut mit
`-ldflags "-X main.openSubtitlesKey=<Key>"`. Den Key nie ins Repository schreiben. Genauso fehlt der TheTVDB-Key
(`-X main.tvdbKey=<Key>`, eigener Key unter [thetvdb.com/api-information](https://thetvdb.com/api-information)).

CI prüft bei jedem Push und zusätzlich jeden Montag:

- `go test` inklusive Sicherheitstests (`security_test.go`)
- `govulncheck`: bekannte Sicherheitslücken in Go und im Code
- `staticcheck` und `gosec`: statische Analyse (bewusste Ausnahmen sind mit `#nosec` und Begründung markiert)
- Barrierefreiheit nach WCAG 2.2 AA mit Playwright und axe, hell und dunkel, inklusive Tastaturbedienung und 320 px Breite:
  `cd e2e && npm ci && npx playwright install chromium && npm test`

Den Screenshot oben erzeugt `cd e2e && npm run screenshot` neu.

## TMDB

<a href="https://www.themoviedb.org"><img src="web/tmdb.svg" alt="The Movie Database (TMDB)" height="16"></a>

This product uses the TMDB API but is not endorsed or certified by TMDB.
Dieses Produkt nutzt die TMDB-API, wird aber von TMDB weder unterstützt noch zertifiziert.

Der Hinweis und das Logo stehen auch im Programm unten im Bereich „Über OrganiBear“, dort ebenso die Hinweise auf
[TheTVDB.com](https://thetvdb.com) und [OpenSubtitles.com](https://www.opensubtitles.com).
Die kostenlose TMDB-API ist nur für nicht-kommerzielle Nutzung gedacht; jeder Nutzer trägt seinen eigenen API-Key ein.

## Release veröffentlichen

Ein Tag startet den Release-Workflow. Er testet, baut Windows, macOS (Intel und Apple Silicon)
und Linux (amd64 und arm64, jeweils mit Fenster und ohne) und legt ein GitHub-Release mit den Programmen,
`THIRD_PARTY_NOTICES.txt` (Lizenzen der eingebauten Bibliotheken) und `SHA256SUMS` an.
Die Version steht danach im Startbanner. Tags mit Bindestrich (`v0.2.0-rc1`) werden als Vorabversion markiert.

```sh
git tag v0.1.0
git push origin v0.1.0
```

Damit das Release Untertitel laden kann, muss vorher das Repository-Secret `OPENSUBTITLES_API_KEY` gesetzt sein
(Settings › Secrets and variables › Actions). Den Key gibt es unter „API consumers“ auf opensubtitles.com.
Für TheTVDB kommt das Secret `THETVDB_API_KEY` dazu (Key unter thetvdb.com › Dashboard › API Keys).
Fehlt eins, baut der Workflow trotzdem und warnt, die Funktion ist im Release dann ausgeblendet.
Nach dem ersten Release bittet OpenSubtitles darum, die App mit ihrem User-Agent (`OrganiBear v0.1.0`) zu melden.

## Lizenz

OrganiBear steht unter der [MIT-Lizenz](LICENSE). Das TMDB-Logo (`web/tmdb.svg`) ist eine Marke von TMDB, das TheTVDB-Logo (`web/thetvdb.png`) eine Marke von TheTVDB.com; beide fallen nicht unter die MIT-Lizenz.

Das Programmfenster nutzt [Wails](https://github.com/wailsapp/wails) (MIT) und weitere Go-Bibliotheken unter MIT-, BSD- und Apache-2.0-Lizenz. Ihre Lizenztexte liegen jedem Release als `THIRD_PARTY_NOTICES.txt` bei (`scripts/notices.sh`).
