// Erzeugt docs/screenshot.png für die README.
//
// Legt Dummy-Dateien in einem neutralen Ordner an, startet OrganiBear,
// schnüffelt und fotografiert die Vorschau. Das Fenster wird so hoch gemacht wie
// die Seite, damit die Auswahlleiste unten sitzt statt mitten in der Liste.
//
//   cd e2e && npm run screenshot
//   OB_SHOT_DIR=/media/Filme npm run screenshot   # anderer Ordner im Bild

import { execFileSync, spawn } from "node:child_process";
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync, existsSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";

const repo = join(dirname(fileURLToPath(import.meta.url)), "..");
const tmp = mkdtempSync(join(tmpdir(), "organibear-shot-"));
const base = process.env.OB_SHOT_DIR || join(tmp, "Medien");
if (existsSync(base) && process.env.OB_SHOT_DIR) {
  console.error(`${base} existiert schon. Bitte einen neuen, leeren Ordner angeben.`);
  process.exit(1);
}
const src = join(base, "Downloads");
const dst = join(base, "Bibliothek");

// Zwei Dateien sind echte Mini-Videos aus testdata/media, damit die Angaben
// aus der Datei und der Duplikat-Vorschlag im Bild sind.
const real = {
  "The.Matrix.1999.2160p.UHD.BluRay.x265-GRP/The.Matrix.1999.2160p.UHD.BluRay.x265-GRP.mkv": "hevc-hdr10.mkv",
  "The Matrix (1999).mkv": "av1.mkv",
};
const files = [
  ...Object.keys(real),
  "The.Matrix.1999.2160p.UHD.BluRay.x265-GRP/Subs/English.srt",
  "Breaking.Bad.S01E03.720p.HDTV.x264.mkv",
  "Breaking.Bad.S01E03.720p.HDTV.x264.de.srt",
  "[Grp] Dark - Staffel 1 Folge 4.mkv",
  "Stranger Things/Staffel 2/05 - Dig Dug.mkv",
  "Inception (2010) [720p].mp4",
  "Amelie.German.720p.WEB-DL.mkv",
  "Titanic.1997.CD1.avi",
  "Titanic.1997.CD2.avi",
];
for (const f of files) {
  mkdirSync(dirname(join(src, f)), { recursive: true });
  writeFileSync(join(src, f), real[f] ? readFileSync(join(repo, "testdata", "media", real[f])) : "");
}

const bin = join(tmp, process.platform === "win32" ? "ob.exe" : "ob");
execFileSync("go", ["build", "-ldflags", "-X main.openSubtitlesKey=e2e-dummy -X main.tvdbKey=e2e-dummy", "-o", bin, "."], { cwd: repo, stdio: "inherit" });
const proc = spawn(bin, ["-config", join(tmp, "cfg.json"), "-no-browser", "-addr", "127.0.0.1:0"]);

try {
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

  const browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 1200, height: 800 }, colorScheme: "light" });
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.goto(url);
  await page.fill("#src", src);
  await page.fill("#dst", dst);
  await page.click("#scan");
  await page.waitForSelector(".item");
  await page.evaluate(() => document.activeElement?.blur());
  const height = await page.evaluate(() => document.documentElement.scrollHeight);
  await page.setViewportSize({ width: 1200, height });
  await page.waitForTimeout(300);
  await page.screenshot({ path: join(repo, "docs", "screenshot.png") });
  await browser.close();
  console.log("docs/screenshot.png geschrieben. ʕ•ᴥ•ʔ");
} finally {
  proc.kill();
  rmSync(tmp, { recursive: true, force: true });
  if (process.env.OB_SHOT_DIR) rmSync(base, { recursive: true, force: true });
}
