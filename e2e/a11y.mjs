// Barrierefreiheits-Test für das Webinterface.
//
// Baut OrganiBear, startet es mit Dummy-Dateien in einem Temp-Ordner und prüft
// alle Ansichten im hellen und dunklen Modus mit axe (WCAG 2.2 AA), dazu
// Tastaturbedienung und Reflow bei 320 px. Beendet sich mit Code 1 bei Fehlern.
//
//   cd e2e && npm ci && npx playwright install chromium && npm test

import { execFileSync, spawn } from "node:child_process";
import { mkdtempSync, mkdirSync, writeFileSync, copyFileSync, rmSync, existsSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";
import { AxeBuilder } from "@axe-core/playwright";

const repo = join(dirname(fileURLToPath(import.meta.url)), "..");
const tmp = mkdtempSync(join(tmpdir(), "organibear-a11y-"));
const failures = [];
const fail = msg => { failures.push(msg); console.log("  ✗ " + msg); };
const ok = msg => console.log("  ✓ " + msg);

// Dummy-Medien: leere Dateien, darunter eine Doppelte und ein Untertitel.
const files = [
  "The.Matrix.1999.1080p.BluRay.x264-GRP/The.Matrix.1999.1080p.BluRay.x264-GRP.mkv",
  "The.Matrix.1999.1080p.BluRay.x264-GRP/Subs/English.srt",
  "Breaking.Bad.S01E03.720p.HDTV.x264.mkv",
  "Breaking.Bad.S01E03.720p.HDTV.x264.de.srt",
  "[Grp] Dark - Staffel 1 Folge 4.mkv",
  "Amelie.2001.mkv",
  "a/Amelie.2001.720p.mkv",
];
function makeFixtures() {
  const src = join(tmp, "in");
  rmSync(src, { recursive: true, force: true });
  rmSync(join(tmp, "out"), { recursive: true, force: true });
  for (const f of files) {
    mkdirSync(dirname(join(src, f)), { recursive: true });
    writeFileSync(join(src, f), "");
  }
  return src;
}

async function startServer() {
  const bin = join(tmp, process.platform === "win32" ? "ob.exe" : "ob");
  execFileSync("go", ["build", "-ldflags", "-X main.openSubtitlesKey=e2e-dummy -X main.tvdbKey=e2e-dummy", "-o", bin, "."], { cwd: repo, stdio: "inherit" });
  const proc = spawn(bin, ["-config", join(tmp, "cfg.json"), "-no-browser", "-addr", "127.0.0.1:0"]);
  const url = await new Promise((resolve, reject) => {
    let out = "";
    proc.stdout.on("data", d => {
      out += d;
      const m = out.match(/http:\/\/\S+\?t=\w+/);
      if (m) resolve(m[0]);
    });
    proc.on("exit", code => reject(new Error("Server beendet mit " + code + "\n" + out)));
    setTimeout(() => reject(new Error("Server startet nicht:\n" + out)), 20000);
  });
  return { proc, url };
}

async function axe(page, label) {
  const res = await new AxeBuilder({ page })
    .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa", "best-practice"])
    .analyze();
  if (res.violations.length === 0) return ok(`axe: ${label}`);
  for (const v of res.violations) {
    fail(`axe ${label}: ${v.id} (${v.impact}) – ${v.help} – ${v.nodes.slice(0, 3).map(n => n.target.join(" ")).join(" | ")}`);
  }
}

async function scan(page, src) {
  await page.fill("#src", src);
  await page.fill("#dst", join(tmp, "out"));
  await page.click("#scan");
  await page.waitForSelector(".item");
  await page.waitForTimeout(200);
}

const focused = page => page.evaluate(() => {
  const a = document.activeElement;
  return a === document.body ? "body" : (a.id || a.className || a.tagName);
});

async function run() {
  const { proc, url } = await startServer();
  const browser = await chromium.launch();
  try {
    // Erster Start ohne TMDB-Key: Die Anleitung öffnet sich von selbst.
    console.log("\nErster Start (Anleitung zum TMDB-Key)");
    for (const scheme of ["light", "dark"]) {
      const context = await browser.newContext({ viewport: { width: 1280, height: 800 }, colorScheme: scheme });
      await context.grantPermissions(["clipboard-read", "clipboard-write"]);
      const page = await context.newPage();
      page.on("pageerror", e => fail("JavaScript-Fehler: " + e.message));
      await page.goto(url);
      await page.waitForSelector("#guide[open]", { timeout: 5000 }).then(() => ok(`Anleitung öffnet sich (${scheme})`), () => fail("Anleitung öffnet sich nicht"));
      for (let step = 1; step <= 4; step++) {
        (await focused(page)).includes("g-title") || fail(`Schritt ${step}: Fokus nicht auf der Überschrift`);
        await axe(page, `Anleitung Schritt ${step} (${scheme})`);
        if (step === 3) {
          await page.click('[data-copy="g-url"]');
          const clip = await page.evaluate(() => navigator.clipboard.readText());
          clip === "https://github.com/DGne-Nurag/OrganiBear" ? ok("Kopieren-Knopf") : fail("Zwischenablage: " + clip);
        }
        if (step < 4) await page.click("#g-next");
      }
      // Key prüfen: TMDB-Antwort nachgestellt, damit der Test nicht ins Internet muss.
      await page.click("#g-next");
      (await page.getAttribute("#g-key", "aria-invalid")) === "true" ? ok("Leerer Key wird angemahnt") : fail("Leerer Key ohne Hinweis");
      await page.route("**/api/tmdb/test", r => r.fulfill({ status: 502, contentType: "application/json", body: '{"error":"TMDB lehnt den API-Key ab"}' }));
      await page.fill("#g-key", "falsch");
      await page.click("#g-next");
      await page.waitForFunction(() => document.querySelector("#g-msg").textContent.includes("kennt diesen Key nicht"))
        .then(() => ok("Falscher Key: verständliche Meldung"), () => fail("Falscher Key: " + "keine Meldung"));
      await axe(page, `Anleitung mit Fehler (${scheme})`);
      await page.unroute("**/api/tmdb/test");
      await page.route("**/api/tmdb/test", r => r.fulfill({ status: 200, contentType: "application/json", body: '{"message":"Der Key passt."}' }));
      await page.fill("#g-key", "0123456789abcdef0123456789abcdef");
      await page.keyboard.press("Enter");
      const title = () => page.textContent("#g-title");
      await page.waitForFunction(() => document.querySelector("#g-title").textContent === "Der Key passt!")
        .then(() => ok("Richtiger Key: gespeichert"), () => fail("Richtiger Key: kein Erfolgs-Schritt"));
      await axe(page, `Key gespeichert (${scheme})`);
      (await page.inputValue("#key")) === "0123456789abcdef0123456789abcdef" ? ok("Key steht in den Einstellungen") : fail("Key fehlt in den Einstellungen");
      // Aussehen der Bibliothek wählen
      await page.click("#g-next");
      await page.waitForSelector('#g-formats input[value="flat"]');
      (await page.isChecked('#g-formats input[value="plex"]')) ? ok("Plex-Vorlage ist vorausgewählt") : fail("Keine Vorauswahl");
      (await page.textContent("#g-formats")).includes("Der Herr der Ringe") ? ok("Beispiele im Ordnerbaum") : fail("Keine Beispiele");
      await axe(page, `Aussehen wählen (${scheme})`);
      await page.check('#g-formats input[value="flat"]');
      await page.click("#g-next");
      await page.waitForFunction(() => document.querySelector("#g-title").textContent.startsWith("Soll ich"));
      (await page.inputValue("#tmovie")) === "{title} ({year})" ? ok("Gewählte Vorlage gespeichert") : fail("Vorlage: " + await page.inputValue("#tmovie"));
      await axe(page, `Tour anbieten (${scheme})`);
      await page.click("#g-next");
      for (let n = 1; n <= 5; n++) {
        const loaded = await page.evaluate(i => { const img = document.querySelector(`[data-step="tour${i}"] img`); return img.complete && img.naturalWidth > 0; }, n);
        loaded ? ok(`Tour-Bild ${n}`) : fail(`Tour-Bild ${n} fehlt`);
        if (n === 1) await axe(page, `Tour (${scheme})`);
        await page.click("#g-next");
      }
      (await page.locator("#guide").isVisible()) ? fail("Tour bleibt offen") : ok("Los geht's schließt die Tour");
      await page.click("#help");
      (await title()) === "Ordner aussuchen" ? ok("Hilfe öffnet die Tour") : fail("Hilfe: " + await title());
      await page.click("#g-later");
      await page.click('nav button[data-tab="settings"]');
      await page.click("[data-format]");
      await page.waitForSelector('#g-formats input[value="flat"]:checked');
      ok("Aussehen in den Einstellungen wieder wählbar");
      await page.check('#g-formats input[value="plex"]');
      await page.click("#g-next");
      await page.waitForFunction(() => !document.querySelector("#guide").open, null, { timeout: 5000 })
        .then(() => ok("Aussehen gespeichert und geschlossen"), () => fail("Aussehen-Dialog bleibt offen"));
      await page.click('nav button[data-tab="sort"]');
      // Key wieder entfernen: Die restlichen Tests laufen ohne TMDB.
      await page.evaluate(async () => {
        const c = (await (await fetch("/api/config")).json()).config;
        await fetch("/api/config", { method: "PUT", headers: { "X-OrganiBear": "1" }, body: JSON.stringify({ ...c, tmdb_api_key: "" }) });
      });
      await page.reload();
      await page.waitForSelector("#guide[open]");
      await page.click("#g-later");
      // Ohne Key geht es trotzdem mit dem Aussehen weiter.
      await page.waitForFunction(() => document.querySelector("#g-title").textContent.startsWith("Wie soll"))
        .then(() => ok("„Später“ führt zum Aussehen"), () => fail("„Später“ überspringt das Aussehen"));
      await page.click("#g-next");
      await page.waitForFunction(() => document.querySelector("#g-title").textContent.startsWith("Soll ich"));
      await page.click("#g-later");
      await page.reload();
      await page.waitForTimeout(500);
      (await page.locator("#guide").isVisible()) ? fail("„Später“ wird nicht gemerkt") : ok("„Später“ wird gemerkt");
      await page.click('#offline-hint [data-guide]');
      (await page.locator("#guide").isVisible()) ? ok("Anleitung über den Hinweis erreichbar") : fail("Hinweis öffnet die Anleitung nicht");
      await page.keyboard.press("Escape");
      // Für den zweiten Durchlauf wieder wie beim ersten Start.
      await page.evaluate(async skip => {
        const c = (await (await fetch("/api/config")).json()).config;
        await fetch("/api/config", { method: "PUT", headers: { "X-OrganiBear": "1" }, body: JSON.stringify({ ...c, tmdb_guide_skipped: skip }) });
      }, scheme === "dark");
      await context.close();
    }

    for (const scheme of ["light", "dark"]) {
      console.log(`\n${scheme === "light" ? "Heller" : "Dunkler"} Modus`);
      const src = makeFixtures();
      const context = await browser.newContext({ viewport: { width: 1280, height: 800 }, colorScheme: scheme });
      const page = await context.newPage();
      page.on("dialog", d => d.accept());
      page.on("pageerror", e => fail("JavaScript-Fehler: " + e.message));
      await page.goto(url);
      await axe(page, "Start");

      await scan(page, src);
      await page.click(".item .tog");
      await axe(page, "Sortieren mit offenem Anpassen");

      // Tastatur: Fokus bleibt beim Abhaken und springt ins geöffnete Formular.
      await page.focus(".item:nth-child(2) .sel");
      await page.keyboard.press("Space");
      const f1 = await focused(page);
      f1.includes("sel") ? ok("Fokus bleibt auf der Checkbox") : fail("Fokus nach Checkbox: " + f1);
      await page.focus(".item:nth-child(3) .tog");
      await page.keyboard.press("Enter");
      await page.waitForTimeout(100);
      const f2 = await focused(page);
      f2.includes("m-title") ? ok("Fokus springt ins Anpassen-Formular") : fail("Fokus nach Anpassen: " + f2);

      await page.click('nav button[data-tab="settings"]');
      await page.waitForTimeout(300);
      await axe(page, "Einstellungen");
      (await page.locator("#tvdb-card").isVisible()) ? ok("TheTVDB-Einstellungen sichtbar") : fail("TheTVDB-Einstellungen fehlen");

      // Einstellungen sichern und wieder einlesen, beides über den Ordnerdialog.
      const stick = join(tmp, "stick-" + scheme);
      mkdirSync(stick, { recursive: true });
      for (const [btn, want] of [["#cfg-export", "Gesichert in"], ["#cfg-import", "Eingelesen aus"]]) {
        await page.click(btn);
        await page.waitForTimeout(300);
        if (btn === "#cfg-export") await axe(page, "Ordnerdialog zum Sichern");
        await page.evaluate(d => openDir(d), stick);
        await page.waitForTimeout(300);
        await page.click("#pk-ok");
        await page.waitForTimeout(500);
        const msg = await page.textContent("#io-result");
        msg.includes(want) ? ok(want.split(" ")[0] + ": " + msg.slice(0, 60)) : fail(btn + ": " + msg);
      }
      existsSync(join(stick, "OrganiBear-Einstellungen.json")) ? ok("Sicherungsdatei liegt im Ordner") : fail("Sicherungsdatei fehlt");

      await page.click('nav button[data-tab="sort"]');
      await page.click('[data-pick="src"]');
      await page.waitForTimeout(300);
      const f3 = await focused(page);
      f3 !== "body" ? ok("Ordnerdialog setzt den Fokus") : fail("Ordnerdialog ohne Fokus");
      await axe(page, "Ordnerdialog");
      await page.keyboard.press("Escape");

      await page.click("#apply");
      await page.waitForTimeout(500);
      await axe(page, "nach dem Einsortieren");
      await page.click('nav button[data-tab="history"]');
      await page.waitForTimeout(300);
      await axe(page, "Verlauf");

      await page.setViewportSize({ width: 320, height: 700 });
      await page.click('nav button[data-tab="sort"]');
      await page.waitForTimeout(200);
      const sw = await page.evaluate(() => document.documentElement.scrollWidth);
      sw <= 320 ? ok("kein seitliches Scrollen bei 320 px") : fail(`seitliches Scrollen bei 320 px (Breite ${sw})`);

      // Online-Zustand mit Treffern simulieren, damit auch diese Ansicht geprüft wird.
      await page.setViewportSize({ width: 1280, height: 800 });
      await page.route("**/api/items", r => r.fulfill({ json: { online: true, items: [
        { id: 1, source: "/x/a.mkv", target: "/y/a.mkv", rel_source: "The.Matrix.1999.mkv", parsed: { title: "The Matrix", year: 1999 },
          info: { title: "Matrix", year: 1999, tmdb_id: 603 }, matched: true, action: "move", companions: [],
          rel_target: "Filme/Matrix (1999)/Matrix (1999).mkv", status: "ready",
          candidates: [{ id: 603, title: "Matrix", year: 1999 }, { id: 604, title: "Matrix Reloaded", year: 2003 }] },
        { id: 2, source: "/x/b.mkv", target: "/y/b.mkv", rel_source: "Foo.mkv", parsed: { title: "Foo" }, info: { title: "Foo" },
          candidates: [], matched: false, action: "copy", companions: [], rel_target: "Filme/Foo/Foo.mkv",
          status: "unmatched", message: "Nichts in der Datenbank gefunden" },
        { id: 3, source: "/x/c.mkv", target: "/y/c.mkv", rel_source: "One.Piece.E1071.mkv", parsed: { title: "One Piece", series: true },
          info: { title: "One Piece", year: 1999, series: true, episode: 1071, absolute: 1071, tmdb_id: 37854 }, candidates: [], matched: true,
          action: "move", companions: [], rel_target: "Serien/One Piece (1999)/Staffel/One Piece - SE1071.mkv", status: "unmatched",
          message: "Folge 1071 ist fortlaufend gezählt. Bitte unter „Anpassen“ Staffel und Folge wählen." },
        { id: 4, source: "/x/d.mkv", target: "/y/d.mkv", rel_source: "Hoshi no Kuma - 13.mkv", parsed: { title: "Hoshi no Kuma", series: true },
          info: { title: "Der Sternenbär", year: 2019, series: true, season: 2, episode: 1, episode_title: "Winterschlaf", tvdb_id: 424242, source: "tvdb" },
          candidates: [{ id: 424242, source: "tvdb", title: "Der Sternenbär", year: 2019 }], matched: true, action: "move", companions: [],
          rel_target: "Serien/Der Sternenbär (2019)/Staffel 02/Der Sternenbär - S02E01 - Winterschlaf.mkv", status: "ready",
          message: "Folge 13 ist fortlaufend gezählt, laut TheTVDB ist das Staffel 2, Folge 1. Bitte kurz prüfen." },
      ] } }));
      await page.route("**/api/tv/37854/seasons", r => r.fulfill({ json: [{ number: 0, name: "Specials", episodes: 40 }, { number: 21, name: "Wano Kuni", episodes: 197 }] }));
      await page.route("**/api/tv/37854/season/21", r => r.fulfill({ json: [{ number: 179, name: "Ruffy gegen Kaido" }] }));
      await page.reload();
      await page.waitForSelector(".item");
      await page.click(".item .tog");
      await axe(page, "Treffer und Suche");
      (await page.locator('.item[data-id="4"] a.tag[href^="https://thetvdb.com/"]').count()) === 1
        ? ok("TheTVDB-Treffer mit Link auf TheTVDB.com") : fail("Link auf TheTVDB.com fehlt beim Treffer");
      {
        // Die Auswahlleiste steht am Seitenende direkt unter der Liste, nicht unter dem Footer.
        await page.evaluate(() => window.scrollTo(0, document.body.scrollHeight));
        const [bar, list, foot] = await page.evaluate(() => ["#bar", "#results", "footer.about"].map(s => document.querySelector(s).getBoundingClientRect().toJSON()));
        bar.top >= list.bottom - 1 && bar.bottom <= foot.top ? ok("Auswahlleiste unter der Liste, über dem Footer")
          : fail(`Auswahlleiste liegt falsch (Liste bis ${list.bottom}, Leiste ${bar.top}-${bar.bottom}, Footer ab ${foot.top})`);
        await page.evaluate(() => window.scrollTo(0, 0));
      }
      await page.click('.item[data-id="3"] .tog');
      await page.selectOption('.item[data-id="3"] .ps', "21");
      await page.waitForSelector('.item[data-id="3"] .pe:not([disabled])');
      const eps = await page.$$eval('.item[data-id="3"] .pe option', o => o.map(x => x.textContent));
      eps.includes("179. Ruffy gegen Kaido") ? ok("Folgenliste aus TMDB") : fail("Folgenliste: " + eps.join(" | "));
      await axe(page, "Staffel- und Folgenauswahl");
      await context.close();
    }

    // Programmfenster: Wails stellt window.runtime bereit. Hier nachgebaut, um
    // Drag & Drop auf Quelle und Ziel und das Öffnen externer Links zu prüfen.
    console.log("\nNur umbenennen");
    {
      const context = await browser.newContext({ viewport: { width: 1280, height: 800 } });
      const page = await context.newPage();
      page.on("dialog", d => d.accept());
      page.on("pageerror", e => fail("JavaScript-Fehler: " + e.message));
      await page.goto(url);
      const src = makeFixtures();
      await page.check('input[name="mode"][value="inplace"]');
      (await page.locator("#dst").isVisible()) ? fail("Zielfeld bleibt sichtbar") : ok("Zielfeld ausgeblendet");
      (await page.evaluate(() => !document.querySelector("#empty-inplace").hidden && document.querySelector("#empty-sort").hidden))
        ? ok("Leere Liste erklärt das Umbenennen") : fail("Leere Liste spricht noch von Ziel und Verschieben");
      await axe(page, "Nur umbenennen");
      await page.fill("#src", src);
      await page.click("#scan");
      await page.waitForSelector(".item");
      await page.click("#apply");
      await page.waitForFunction(() => /umbenannt/.test(document.querySelector("#say").textContent), null, { timeout: 5000 })
        .catch(() => fail("keine Fertig-Meldung: " + "umbenannt"));
      existsSync(join(src, "Filme")) && !existsSync(join(tmp, "out", "Filme")) ? ok("Dateien bleiben im Quellordner") : fail("Dateien wurden verschoben");
      await page.reload();
      (await page.isChecked('input[name="mode"][value="inplace"]')) ? ok("Auswahl wird gemerkt") : fail("Auswahl vergessen");
      await page.check('input[name="mode"][value="sort"]');
      await page.waitForTimeout(300);
      await page.evaluate(async () => {
        const c = (await (await fetch("/api/config")).json()).config;
        await fetch("/api/config", { method: "PUT", headers: { "X-OrganiBear": "1" }, body: JSON.stringify({ ...c, in_place: false }) });
      });
      await context.close();
    }

    console.log("\nMP4 in MKV umpacken");
    {
      const context = await browser.newContext({ viewport: { width: 1280, height: 800 } });
      const page = await context.newPage();
      page.on("dialog", d => d.accept());
      page.on("pageerror", e => fail("JavaScript-Fehler: " + e.message));
      await page.goto(url);
      await page.click('[data-tab="settings"]');
      await page.check("#r-on");
      /Noch nicht gespeichert/.test(await page.textContent("#save-msg")) ? ok("Hinweis: noch nicht gespeichert") : fail("Kein Hinweis auf ungespeicherte Änderung");
      await axe(page, "Einstellungen mit Umpacken");
      await page.click("#save");
      await page.waitForFunction(() => /✓ Gespeichert um/.test(document.querySelector("#save-msg").textContent), null, { timeout: 5000 })
        .then(() => ok("Speichern bestätigt neben dem Knopf"), () => fail("Keine Bestätigung beim Speichern"));
      await axe(page, "Einstellungen gespeichert");
      await page.click('[data-tab="sort"]');
      const src = join(tmp, "mp4");
      rmSync(src, { recursive: true, force: true });
      rmSync(join(tmp, "out"), { recursive: true, force: true });
      mkdirSync(src, { recursive: true });
      copyFileSync(join(repo, "testdata", "remux", "h264aac.mp4"), join(src, "Inception.2010.mp4"));
      writeFileSync(join(src, "Amelie.2001.mp4"), "");
      await scan(page, src);
      const tags = await page.textContent("#results");
      /wird zu MKV umgepackt/.test(tags) ? ok("Vorschau zeigt das Umpacken") : fail("Vorschau ohne Umpack-Hinweis");
      /Bleibt MP4/.test(tags) ? ok("Kaputte MP4 bleibt MP4, mit Grund") : fail("Kein Hinweis bei nicht umpackbarer MP4");
      await axe(page, "Liste mit Umpacken");
      await page.click("#selready");
      await page.click("#apply");
      await page.waitForFunction(() => /Papierkorb/.test(document.querySelector("#say").textContent), null, { timeout: 10000 })
        .then(() => ok("Fertig-Meldung nennt den Papierkorb"), () => fail("Fertig-Meldung ohne Papierkorb"));
      existsSync(join(tmp, "out", "OrganiBear-Papierkorb", "Inception.2010.mp4")) ? ok("Original im Papierkorb") : fail("Original nicht im Papierkorb");
      await page.evaluate(async () => {
        const c = (await (await fetch("/api/config")).json()).config;
        await fetch("/api/config", { method: "PUT", headers: { "X-OrganiBear": "1" }, body: JSON.stringify({ ...c, remux_mp4: false }) });
      });
      await context.close();
    }

    console.log("\nEinzelne Videos");
    {
      const context = await browser.newContext({ viewport: { width: 1280, height: 800 } });
      const page = await context.newPage();
      page.on("pageerror", e => fail("JavaScript-Fehler: " + e.message));
      await page.goto(url);
      const src = makeFixtures();
      await page.fill("#src", src);
      await page.click("#src-pick [data-pickfiles]");
      await page.waitForSelector("#picker[open] #pk-dirs button");
      await page.click(`#pk-dirs [data-dir="${src}"]`).catch(() => {});
      await page.evaluate(d => document.querySelector("#pk-path").dataset.path === d, src);
      await page.waitForSelector(`#pk-dirs input[data-file="${join(src, files[2])}"]`);
      await axe(page, "Videos wählen");
      await page.check(`#pk-dirs input[data-file="${join(src, files[2])}"]`);
      await page.check(`#pk-dirs input[data-file="${join(src, files[5])}"]`);
      (await page.textContent("#pk-ok")) === "2 Videos nehmen" ? ok("Knopf nennt die Anzahl") : fail("Knopf: " + (await page.textContent("#pk-ok")));
      await page.click("#pk-ok");
      (await page.locator("#files li").count()) === 2 ? ok("Zwei Videos in der Liste") : fail("Liste hat nicht zwei Einträge");
      (await page.locator("#src").isVisible()) ? fail("Ordnerfeld bleibt sichtbar") : ok("Ordnerfeld ausgeblendet");
      await axe(page, "Liste einzelner Videos");
      await page.fill("#dst", join(tmp, "out"));
      await page.click("#scan");
      await page.waitForSelector(".item");
      await page.waitForTimeout(200);
      (await page.locator(".item").count()) === 2 ? ok("Nur die gewählten Videos in der Vorschau") : fail("Vorschau: " + (await page.locator(".item").count()) + " Einträge");
      await page.click('[data-unfile="0"]');
      (await page.locator("#files li").count()) === 1 && (await focused(page)).includes("btn") ? ok("Entfernen, Fokus bleibt in der Liste") : fail("Entfernen klappt nicht");
      await page.click("#files-clear");
      (await page.locator("#src").isVisible()) && (await page.inputValue("#src")) === src ? ok("Zurück zum Ordner, der Pfad ist noch da") : fail("Ordnerfeld kommt nicht zurück");
      await context.close();
    }

    console.log("\nProgrammfenster (Drag & Drop)");
    {
      const context = await browser.newContext({ viewport: { width: 1280, height: 800 } });
      await context.addInitScript(() => {
        window.runtime = { OnFileDrop: cb => (window.__drop = cb), BrowserOpenURL: u => (window.__opened = u) };
      });
      const page = await context.newPage();
      page.on("pageerror", e => fail("JavaScript-Fehler: " + e.message));
      await page.goto(url);
      await page.waitForFunction(() => window.__drop);
      const dropOn = async (sel, path, want) => {
        const b = await page.locator(sel).boundingBox();
        await page.evaluate(([x, y, p]) => window.__drop(x, y, [p]), [b.x + 10, b.y + 10, path]);
        await page.waitForFunction(([s, w]) => document.querySelector(s).value === w, [sel, want], { timeout: 5000 })
          .catch(() => fail(`${sel}: erwartet ${want}`));
      };
      const dir = makeFixtures();
      await dropOn("#src", dir, dir);
      ok("Ordner auf Quelle gezogen");
      // Eine Datei zählt als ihr Ordner.
      await dropOn("#dst", join(dir, files[0]), dirname(join(dir, files[0])));
      ok("Datei auf Ziel gezogen, ihr Ordner übernommen");
      await page.evaluate(p => window.__drop(1, 1, [p]), tmp);
      (await page.inputValue("#src")) === dir ? ok("Ablegen außerhalb der Felder ändert nichts") : fail("Ablegen außerhalb hat ein Feld geändert");
      // Mehrere Videos auf die Quelle: Sie kommen in die Liste, Untertitel werden aussortiert.
      const box = await page.locator("#src").boundingBox();
      await page.evaluate(([x, y, ps]) => window.__drop(x, y, ps), [box.x + 10, box.y + 10, [join(dir, files[2]), join(dir, files[5]), join(dir, files[3])]]);
      await page.waitForFunction(() => document.querySelectorAll("#files li").length === 2, null, { timeout: 5000 })
        .then(() => ok("Zwei Videos abgelegt, Untertitel weggelassen"), () => fail("Abgelegte Videos fehlen in der Liste"));
      await page.click("#files-clear");
      await page.click('footer p a[href="https://thetvdb.com"]');
      (await page.evaluate(() => window.__opened)) === "https://thetvdb.com/" ? ok("Externer Link öffnet im normalen Browser") : fail("Externer Link bleibt im Programmfenster");
      await axe(page, "Programmfenster");
      await context.close();
    }

    // Zum Schluss: Beenden-Knopf. Danach muss sich das Programm selbst beenden.
    console.log("\nBeenden");
    const context = await browser.newContext({ viewport: { width: 1280, height: 800 } });
    const page = await context.newPage();
    page.on("dialog", d => d.accept());
    page.on("pageerror", e => fail("JavaScript-Fehler: " + e.message));
    await page.goto(url);
    await page.click("#quit");
    await page.waitForSelector("#bye:not([hidden])");
    const f4 = await focused(page);
    f4 === "h-bye" ? ok("Fokus auf der Abschiedsmeldung") : fail("Fokus nach Beenden: " + f4);
    await axe(page, "Beendet");
    const exited = await new Promise(resolve => {
      if (proc.exitCode !== null) return resolve(true);
      proc.once("exit", () => resolve(true));
      setTimeout(() => resolve(false), 5000);
    });
    exited ? ok("Programm hat sich beendet") : fail("Programm läuft nach dem Beenden weiter");
    await context.close();
  } finally {
    await browser.close();
    proc.kill();
    rmSync(tmp, { recursive: true, force: true });
  }
}

run().then(() => {
  if (failures.length) {
    console.log(`\n${failures.length} Problem(e) gefunden.`);
    process.exit(1);
  }
  console.log("\nAlles barrierefrei. ʕ•ᴥ•ʔ");
}).catch(e => {
  console.error(e);
  process.exit(1);
});
