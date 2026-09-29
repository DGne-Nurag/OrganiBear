# Audit: Sicherheit, Barrierefreiheit, Optik

Stand 29.09.2026. Alle Funde unten sind in diesem Stand behoben und, wo möglich, mit Tests abgesichert (`security_test.go`).
Geprüft wurde mit Dummy-Dateien, echten Angriffen gegen den laufenden Server (curl), axe-core 4 in Playwright (hell und dunkel) sowie Tastatur- und Reflow-Tests.

## Sicherheit

Kein kritischer Fund. XSS, Path-Traversal über Vorlagen und Titel, CSRF per Formular und DNS-Rebinding waren bereits abgewehrt und wurden bestätigt.

| Schwere | Fund | Behebung |
|---|---|---|
| Mittel | TMDB-API-Key landete bei Netzwerkfehlern in Fehlermeldungen im UI (die URL enthält den Key) | Fehlermeldung ohne URL (`tmdb.go`) |
| Mittel/Niedrig | Keine Anmeldung: andere Programme oder Benutzer auf demselben Rechner konnten die API mit gefälschtem Host-Header nutzen, mit `-addr 0.0.0.0` sogar aus dem LAN | Zufälliger Schlüssel pro Start im Startlink, getauscht gegen ein HttpOnly/SameSite-Strict-Cookie; `-addr` nur noch Loopback |
| Niedrig/Mittel | Race: Zwischen „existiert das Ziel?“ und `os.Rename` konnte eine neu entstandene Datei überschrieben werden (im Test 2 von 3000 Mal) | Verschieben per Hardlink + Löschen (schlägt atomar fehl, wenn das Ziel existiert); Kopieren nur noch bei anderem Laufwerk |
| Niedrig | Ein manipuliertes Verlaufs-Journal konnte beim Rückgängigmachen beliebige Dateien löschen oder verschieben | Journal wird vor dem Undo geprüft (alles muss in Quell-/Zielordner liegen); Verlauf nur für den eigenen Benutzer lesbar (0700/0600) |
| Niedrig | Symlink im Zielordner lenkte Dateien nach außerhalb; Undo löschte den Symlink | Zielordner wird nach Auflösen von Symlinks geprüft; Aufräumen fasst nur echte Ordner an |
| Niedrig | Nach dem Scan untergeschobene Symlinks als Quelle wurden beim Kopieren verfolgt | Nur normale Dateien werden verschoben oder kopiert |
| Niedrig | Keine Schutz-Header, Oberfläche konnte in fremde Seiten eingebettet werden (Clickjacking) | CSP, `X-Frame-Options: DENY`, `nosniff`, `no-referrer` |
| Niedrig | Keine Server-Timeouts; TMDB-Nachfragen ohne Frist | `ReadHeaderTimeout`/`IdleTimeout`, 30 s Frist für Einzelsuchen |
| Niedrig | Konfiguration über festen `.tmp`-Namen geschrieben (Symlink/Rechte) | Zufällige Temp-Datei mit 0600 |
| Info | Unbegrenzter TMDB-Cache, 4 MB je Antwort | Max. 500 Einträge, 1 MB je Antwort |
| Info | Go 1.24.7 hat inzwischen gepatchte CVEs in der Standardbibliothek | CI baut mit der aktuellen stabilen Go-Version |

## Barrierefreiheit (WCAG 2.2 AA)

Vorher meldete axe 5 Regelverstöße mit über 40 Stellen, dazu kamen manuelle Funde. Nachher: **0 Verstöße** in allen Ansichten, hell und dunkel.

- **Kontraste:** Weiße Schrift auf Honig-Buttons hatte 2,0 : 1, graue Hinweise 4,2 : 1, Status-Etiketten teils unter 3 : 1, Eingabefelder hoben sich kaum ab (1,2 : 1). Neue Farbwerte: alle Texte ≥ 4,5 : 1, Feldränder ≥ 3 : 1, in beiden Modi.
- **Fokus:** Fokusrahmen für Eingaben und Checkboxen fehlte; nach jedem Klick sprang der Fokus an den Seitenanfang. Jetzt sichtbarer Rahmen, Fokus bleibt beim Abhaken, Öffnen, Suchen, Löschen und im Ordnerdialog erhalten und wird nicht mehr von der Auswahlleiste verdeckt.
- **Namen und Beschriftungen:** Symbol-Buttons (📁 👁 ✕) wurden als Emoji vorgelesen, Checkboxen und Regel-Felder hatten keine Namen, das Anpassen-Formular nutzte Platzhalter statt Beschriftungen. Alles hat jetzt sichtbare Labels oder passende `aria-label`s.
- **Rückmeldungen:** Die Sprechblase des Bären, die Auswahlzahl und die Vorlagen-Vorschau werden jetzt vom Screenreader angesagt (Live-Regionen); Fehler in Vorlagen sind mit dem Feld verknüpft.
- **Struktur:** Überschriften für alle Bereiche, aktiver Bereich per `aria-current`, `aria-expanded` am Anpassen-Button, gewählter Treffer per `aria-pressed` und Häkchen statt nur Farbe, Ordnerdialog mit Titel.
- **Bewegung und Zoom:** Animationen des Bären ruhen bei „Bewegung reduzieren“; bei 320 px Breite scrollt nichts mehr seitlich.

## Optik

- Honig-Buttons und aktiver Reiter mit dunkler Schrift, dadurch klarer und lesbarer.
- Dunkelmodus mit eigenen, hellen Statusfarben; native Bedienelemente folgen dem Farbmodus.
- Status-Etiketten über der Liste sind jetzt Filter („1 × bitte prüfen“ zeigt nur diese Einträge).
- Schnüffeln-Button zeigt „Schnüffelt …“, solange gesucht wird.
- Gewählter Treffer mit Häkchen; Ordnerdialog mit Überschrift und dezent gesetztem Pfad.
- Nach einem Neustart ohne gültigen Link erscheint eine freundliche Bär-Seite statt eines Fehlers.
