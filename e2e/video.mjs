// Nimmt das kurze Produktvideo für die Webseite auf (site/organibear.mp4 und
// site/video-poster.jpg). Wie screenshot.mjs: Dummy-Dateien anlegen, OrganiBear
// starten und die echte Oberfläche bedienen. Untertitel, Mauszeiger und die
// Vorher/Nachher-Karten werden nur für die Aufnahme in die Seite gelegt.
// Braucht ffmpeg (OB_FFMPEG oder im PATH) zum Umwandeln in MP4.
//
//   cd e2e && OB_SHOT_DIR=/media/Filme npm run video

import { execFileSync, spawn } from "node:child_process";
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, readdirSync, rmSync, existsSync, statSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, dirname, relative } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";

const repo = join(dirname(fileURLToPath(import.meta.url)), "..");
const tmp = mkdtempSync(join(tmpdir(), "organibear-video-"));
const base = process.env.OB_SHOT_DIR || join(tmp, "Medien");
if (existsSync(base) && process.env.OB_SHOT_DIR) {
  console.error(`${base} existiert schon. Bitte einen neuen, leeren Ordner angeben.`);
  process.exit(1);
}
const src = join(base, "Downloads");
const dst = join(base, "Bibliothek");
const ffmpeg = process.env.OB_FFMPEG || "ffmpeg";

const real = {
  "The.Matrix.1999.2160p.UHD.BluRay.x265-GRP/The.Matrix.1999.2160p.UHD.BluRay.x265-GRP.mkv": "hevc-hdr10.mkv",
  "The Matrix (1999).mkv": "av1.mkv",
};
const files = [
  ...Object.keys(real),
  "Breaking.Bad.S01E03.720p.HDTV.x264.mkv",
  "Breaking.Bad.S01E03.720p.HDTV.x264.de.srt",
  "[Grp] Dark - Staffel 1 Folge 4.mkv",
  "Stranger Things/Staffel 2/05 - Dig Dug.mkv",
  "Inception (2010) [720p].mp4",
  "Titanic.1997.CD1.avi",
  "Titanic.1997.CD2.avi",
];
for (const f of files) {
  mkdirSync(dirname(join(src, f)), { recursive: true });
  writeFileSync(join(src, f), real[f] ? readFileSync(join(repo, "testdata", "media", real[f])) : "");
}

const walk = d => readdirSync(d).flatMap(n => {
  const p = join(d, n);
  return statSync(p).isDirectory() ? walk(p) : [p];
});

const bin = join(tmp, "ob");
execFileSync("go", ["build", "-o", bin, "."], { cwd: repo, stdio: "inherit" });
const proc = spawn(bin, ["-config", join(tmp, "cfg.json"), "-no-browser", "-addr", "127.0.0.1:0"]);

const W = 1280, H = 720;
const esc = s => s.replace(/[&<>]/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;" })[c]);

// Vorher: die Dateien, wie sie im Download-Ordner liegen. Der Kram, den der Bär
// wegräumt (Qualität, Codec, Gruppe …), ist rot markiert.
const junk = /(\.2160p|\.720p|\.UHD|\.BluRay|\.x265|\.x264|\.HDTV|-GRP|\[Grp\] | \[720p\])/g;
const messWin = () => {
  const names = files.filter(f => !f.endsWith(".srt")).map(f => f.split("/").pop());
  const rows = names.map(n => `<li>🎞️ ${esc(n).replace(junk, m => `<span class="junk">${m}</span>`)}</li>`).join("");
  return `<div class="win mess"><div class="tbar"><i></i><i></i><i></i><b>📁 Downloads</b></div><ul>${rows}</ul></div>`;
};
// Nachher: Ordnerbaum der Bibliothek, Zeile für Zeile eingeblendet.
const treeRows = paths => {
  const rows = [], seen = new Set();
  for (const p of paths) {
    const parts = p.split("/");
    parts.forEach((name, i) => {
      const key = parts.slice(0, i + 1).join("/");
      if (seen.has(key)) return;
      seen.add(key);
      const dir = i < parts.length - 1;
      const icon = dir ? "📁" : /\.srt$/.test(name) ? "💬" : "🎬";
      rows.push(`<li class="${dir ? "dir" : "file"}" style="padding-left:${i * 26}px;animation-delay:${rows.length * 0.18}s">${icon} ${esc(name)}</li>`);
    });
  }
  return rows.join("");
};

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
  // Vorbereiten ohne Aufnahme: Anleitung für den ersten Start überspringen.
  const prep = await browser.newPage();
  await prep.goto(url);
  await prep.evaluate(async () => {
    const c = (await (await fetch("/api/config")).json()).config;
    await fetch("/api/config", { method: "PUT", headers: { "X-OrganiBear": "1" }, body: JSON.stringify({ ...c, tmdb_guide_skipped: true }) });
  });
  const cookies = await prep.context().cookies();
  await prep.close();

  const ctx = await browser.newContext({
    viewport: { width: W, height: H }, colorScheme: "light", deviceScaleFactor: 1,
    recordVideo: { dir: tmp, size: { width: W, height: H } },
  });
  await ctx.addCookies(cookies);
  const page = await ctx.newPage();
  await page.goto(url.replace(/\?t=\w+/, ""));
  await page.waitForSelector("#scan");

  // Bühne: Untertitel, Mauszeiger und Vollbild-Karten über der Oberfläche.
  const stage = () => page.evaluate(() => {
    if (document.getElementById("vstage")) return;
    const st = document.createElement("style");
    st.textContent = `
#vcap { position: fixed; left: 50%; bottom: 28px; transform: translateX(-50%); z-index: 99999; background: #3b2616; color: #fff7ea;
  font: 700 26px/1.3 system-ui, sans-serif; padding: 12px 26px; border-radius: 16px; box-shadow: 0 8px 30px #0005; transition: opacity .3s; white-space: nowrap; }
#vcap:empty { opacity: 0; }
#vcur { position: fixed; z-index: 99997; width: 26px; height: 26px; left: 640px; top: 400px; transition: left .7s ease, top .7s ease; pointer-events: none; }
#vcur.click { transform: scale(.8); }
#vcard { position: fixed; inset: 0; z-index: 99998; background: #fff7ea; display: grid; place-items: center; color: #3b2616;
  font: 20px/1.4 system-ui, sans-serif; transition: opacity .5s; }
#vcard[hidden] { display: grid; opacity: 0; pointer-events: none; }
#vcard h2 { font-size: 44px; margin: .2em 0; } #vcard h2 span { color: #d9861a; }
#vcard .sub { font-size: 26px; margin: 0 0 1em; }
#vcard ul { list-style: none; padding: 18px 28px; margin: 0; background: #fff; border: 3px solid #e8d3b5; border-radius: 18px; text-align: left;
  font: 20px/1.6 ui-monospace, monospace; min-width: 640px; }
#vcard img { width: 150px; height: 150px; }
#vcard .ba { display: flex; align-items: center; gap: 28px; }
#vcard .win { background: #fff; border: 3px solid #e8d3b5; border-radius: 16px; box-shadow: 0 14px 40px #7a4a2422; overflow: hidden; text-align: left; }
#vcard .win .tbar { display: flex; align-items: center; gap: 8px; padding: 10px 16px; background: #fdf0da; border-bottom: 2px solid #e8d3b5; font: 700 20px system-ui, sans-serif; }
#vcard .win .tbar i { width: 12px; height: 12px; border-radius: 50%; background: #e8d3b5; display: inline-block; }
#vcard .win .tbar b { margin-left: 8px; }
#vcard .win ul { list-style: none; margin: 0; padding: 12px 18px; border: 0; border-radius: 0; background: none; min-width: 0; font: 18px/1.75 ui-monospace, monospace; }
#vcard .win li { white-space: nowrap; }
#vcard .mess li { color: #3b2616; }
#vcard .mess li:nth-child(odd) { transform: translateX(10px) rotate(-.6deg); }
#vcard .mess li:nth-child(3n) { transform: translateX(-6px) rotate(.8deg); }
#vcard .junk { color: #c0392b; background: #fde2de; border-radius: 4px; padding: 0 2px; }
#vcard .win ul.tree { font-size: 15px; line-height: 1.5; padding: 10px 18px; }
#vcard .tree li { opacity: 0; animation: pop .35s ease forwards; }
#vcard .tree .dir { font-weight: 700; }
#vcard .tree .file { color: #24613d; }
@keyframes pop { from { opacity: 0; transform: translateX(-12px); } to { opacity: 1; transform: none; } }
#vcard .small { transform: scale(.72); transform-origin: right center; opacity: .75; margin-left: -120px; }
#vcard .arrow { display: grid; justify-items: center; gap: 6px; font: 800 20px system-ui, sans-serif; color: #8a5208; }
#vcard .arrow img { width: 84px; height: 84px; }
#vcard .arrow span { font-size: 54px; line-height: 1; }
#vcard .head { font-size: 34px; font-weight: 800; margin: 0 0 18px; text-align: center; }`;
    document.head.append(st);
    const d = document.createElement("div");
    d.id = "vstage";
    d.innerHTML = `<div id="vcap"></div><div id="vcard" hidden></div>
<svg id="vcur" viewBox="0 0 24 24"><path d="M3 2l7 19 2.5-7.5L20 11z" fill="#fff" stroke="#2b1a10" stroke-width="1.6" stroke-linejoin="round"/></svg>`;
    document.body.append(d);
  });
  await stage();
  const cap = t => page.evaluate(t => { document.getElementById("vcap").textContent = t; }, t);
  const card = html => page.evaluate(h => {
    const c = document.getElementById("vcard");
    if (h === null) { c.hidden = true; return; }
    c.innerHTML = h;
    c.hidden = false;
  }, html);
  const moveTo = async sel => {
    const b = await page.locator(sel).first().boundingBox();
    await page.evaluate(([x, y]) => { const c = document.getElementById("vcur"); c.style.left = x + "px"; c.style.top = y + "px"; }, [b.x + b.width / 2, b.y + b.height / 2]);
    await page.waitForTimeout(800);
  };
  const click = async sel => {
    await moveTo(sel);
    await page.evaluate(() => document.getElementById("vcur").classList.add("click"));
    await page.waitForTimeout(150);
    await page.evaluate(() => document.getElementById("vcur").classList.remove("click"));
    await page.click(sel);
  };
  const type = async (sel, text) => {
    await click(sel);
    await page.fill(sel, "");
    await page.type(sel, text, { delay: 35 });
  };
  const wait = ms => page.waitForTimeout(ms);

  // 1. Titel
  await card(`<div style="text-align:center"><img src="icon.png" alt=""><h2>Organi<span>Bear</span></h2>
<p class="sub">Der Bär, der deine Filme und Serien aufräumt.</p></div>`);
  await wait(3000);

  // 2. Vorher
  await card(`<div><p class="head">Vorher: Durcheinander im Download-Ordner 😵</p>${messWin()}</div>`);
  await wait(4500);
  await card(null);
  await wait(600);

  // 3. Ordner wählen
  await cap("1. Sag dem Bären, wo die Videos liegen");
  await type("#src", src);
  await wait(500);
  await cap("… und wo sie hinsollen");
  await type("#dst", dst);
  await wait(900);

  // 4. Schnüffeln
  await cap("2. Auf „Schnüffeln“ drücken");
  await click("#scan");
  await page.waitForSelector(".item");
  await page.evaluate(() => document.activeElement?.blur());
  await cap("Der Bär zeigt dir vorher genau, was passiert");
  await wait(1500);
  const list = await page.locator(".item").first().boundingBox();
  await page.mouse.wheel(0, list.y - 120);
  await wait(1200);
  for (let i = 0; i < 4; i++) { await page.mouse.wheel(0, 160); await wait(700); }
  await wait(800);

  // 5. Einsortieren
  await cap("3. Passt alles? Dann „Einsortieren“");
  page.on("dialog", async d => { await wait(400); await d.accept(); });
  await click("#apply");
  await page.waitForFunction(() => /Fertig/.test(document.querySelector("#say").textContent));
  await page.evaluate(() => window.scrollTo({ top: 0, behavior: "smooth" }));
  await cap("Fertig!");
  await wait(2200);

  // 6. Nachher
  const after = walk(dst).map(p => relative(dst, p)).sort();
  await cap("");
  await card(`<div><p class="head">Nachher: jeder Film und jede Serie in ihrem Ordner 🎉</p>
<div class="ba"><div class="small">${messWin()}</div>
<div class="arrow"><img src="icon.png" alt=""><span>➜</span></div>
<div class="win"><div class="tbar"><i></i><i></i><i></i><b>📁 Bibliothek</b></div><ul class="tree">${treeRows(after)}</ul></div></div></div>`);
  await wait(6500);
  await card(null);
  await wait(500);

  // 7. Verlauf
  await cap("Doch anders gewollt? Mit einem Klick rückgängig");
  await click('nav button[data-tab="history"]');
  await page.waitForSelector(".hist");
  await wait(600);
  await moveTo(".hist button");
  await wait(2200);

  // 8. Abspann
  await cap("");
  await card(`<div style="text-align:center"><img src="icon.png" alt=""><h2>Organi<span>Bear</span></h2>
<p class="sub">Kostenlos für Windows, Mac und Linux.<br>Ein Programm, keine Installation.</p></div>`);
  await wait(3500);

  const video = page.video();
  await ctx.close();
  const raw = await video.path();
  await browser.close();

  const out = join(repo, "site");
  mkdirSync(out, { recursive: true });
  execFileSync(ffmpeg, ["-y", "-loglevel", "error", "-i", raw, "-c:v", "libx264", "-preset", "slow", "-crf", "26", "-pix_fmt", "yuv420p",
    "-movflags", "+faststart", "-an", join(out, "organibear.mp4")], { stdio: "inherit" });
  execFileSync(ffmpeg, ["-y", "-loglevel", "error", "-ss", "0.5", "-i", raw, "-frames:v", "1", "-q:v", "4", join(out, "video-poster.jpg")], { stdio: "inherit" });
  console.log("site/organibear.mp4 und site/video-poster.jpg geschrieben. ʕ•ᴥ•ʔ");
} finally {
  proc.kill();
  rmSync(tmp, { recursive: true, force: true });
  if (process.env.OB_SHOT_DIR) rmSync(base, { recursive: true, force: true });
}
