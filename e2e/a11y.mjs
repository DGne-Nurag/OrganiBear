// Barrierefreiheits-Test für das Webinterface.
//
// Baut OrganiBear, startet es mit Dummy-Dateien in einem Temp-Ordner und prüft
// alle Ansichten im hellen und dunklen Modus mit axe (WCAG 2.2 AA), dazu
// Tastaturbedienung und Reflow bei 320 px. Beendet sich mit Code 1 bei Fehlern.
//
//   cd e2e && npm ci && npx playwright install chromium && npm test

import { execFileSync, spawn } from "node:child_process";
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from "node:fs";
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
      await page.click('.item[data-id="3"] .tog');
      await page.selectOption('.item[data-id="3"] .ps', "21");
      await page.waitForSelector('.item[data-id="3"] .pe:not([disabled])');
      const eps = await page.$$eval('.item[data-id="3"] .pe option', o => o.map(x => x.textContent));
      eps.includes("179. Ruffy gegen Kaido") ? ok("Folgenliste aus TMDB") : fail("Folgenliste: " + eps.join(" | "));
      await axe(page, "Staffel- und Folgenauswahl");
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
