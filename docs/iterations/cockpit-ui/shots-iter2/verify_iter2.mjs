// verify_iter2.mjs — iteration-2 acceptance against the REAL cockpit server.
//
// Two claims have to be checked against real data, not a fixture:
//   1. D2-2: clicking the badge jumps to the condition row that failed, and
//      that row is visibly marked.
//   2. D2-4: the source window renders real code lines for a real finding.
//
// Fixtures proved the renderer; only a live server proves the wiring. The
// browser is driven over CDP the same way cdp_shoot.mjs drives it (one target
// per check, closed after), so a leaked renderer cannot decide the next result.
//
// Usage: node verify_iter2.mjs <outdir> [base] [repo]
//   base defaults to http://127.0.0.1:8891 (start one yourself: the script does
//   not spawn the cockpit server, because the db and the port differ per tree)
//   repo is the scope token the gate/issues pages are opened with; it defaults
//   to the directory this script's repository was scanned from. Pass it
//   explicitly when re-running elsewhere — a scope that matches no run yields an
//   UNKNOWN banner, which fails gate-banner-failed for the wrong reason.
import { spawn, execSync } from "node:child_process";
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";

const outdir = process.argv[2];
const BASE = process.argv[3] || "http://127.0.0.1:8891";if (!outdir) {
  console.error("usage: node verify_iter2.mjs <outdir> [base]");
  process.exit(2);
}
mkdirSync(outdir, { recursive: true });

const CHROME = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";
const PORT = 9344;
const WIDTH = 1600;
const HEIGHT = 1100;
const PROFILE = join(outdir, "profile");
const REPO = process.argv[4] || process.cwd();

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const arm = (() => {
  try {
    return execSync("sysctl -n hw.optional.arm64", { encoding: "utf8" }).trim() === "1";
  } catch {
    return false;
  }
})();

// On Apple Silicon a plain path launch is picked up as the x86_64 slice and
// runs under Rosetta, where --headless=new never opens the CDP port. Force the
// arm64 slice; on Intel this is a no-op.
const LAUNCH = arm ? ["/usr/bin/arch", "-arm64", CHROME] : [CHROME];
const chrome = spawn(
  LAUNCH[0],
  [
    ...LAUNCH.slice(1),
    "--headless=new",
    `--remote-debugging-port=${PORT}`,
    `--user-data-dir=${PROFILE}`,
    "--disable-gpu",
    "--hide-scrollbars",
    "--no-first-run",
    "--no-default-browser-check",
    "--disable-extensions",
    "--disable-background-networking",
    "--window-size=" + WIDTH + "," + HEIGHT,
  ],
  { stdio: "ignore" },
);
const shutdown = () => {
  try {
    chrome.kill("SIGKILL");
  } catch {}
};
process.on("exit", shutdown);
process.on("SIGINT", () => (shutdown(), process.exit(130)));

async function connect(url) {
  const ws = new WebSocket(url);
  await new Promise((res, rej) => {
    ws.onopen = res;
    ws.onerror = () => rej(new Error("ws failed " + url));
  });
  const pending = new Map();
  let nextId = 1;
  ws.onmessage = (ev) => {
    const msg = JSON.parse(ev.data);
    if (msg.id !== undefined && pending.has(msg.id)) {
      const { resolve, reject, timer } = pending.get(msg.id);
      pending.delete(msg.id);
      clearTimeout(timer);
      msg.error ? reject(new Error(msg.error.message)) : resolve(msg.result);
    }
  };
  return {
    send: (method, params = {}, sessionId) =>
      new Promise((resolve, reject) => {
        const id = nextId++;
        const timer = setTimeout(() => {
          pending.delete(id);
          reject(new Error("CDP " + method + " timed out"));
        }, 30000);
        pending.set(id, { resolve, reject, timer });
        ws.send(JSON.stringify({ id, method, params, ...(sessionId ? { sessionId } : {}) }));
      }),
    close: () => {
      try {
        ws.close();
      } catch {}
    },
  };
}

const failures = [];
const check = (name, ok, detail) => {
  if (!ok) failures.push(`${name}: ${detail}`);
  // The detail is the diagnosis, so it is only printed when it explains a
  // failure. Printing it on PASS reads as an objection to a passing check.
  console.log(`${ok ? "PASS" : "FAIL"} ${name}${!ok && detail ? " — " + detail : ""}`);
};

/** open navigates a fresh target to url and returns an evaluate helper. */
async function open(cdp, url) {
  const { targetId } = await cdp.send("Target.createTarget", { url: "about:blank" });
  const { sessionId } = await cdp.send("Target.attachToTarget", { targetId, flatten: true });
  const session = (m, p) => cdp.send(m, p, sessionId);
  await session("Page.enable");
  await session("Runtime.enable");
  await session("Emulation.setDeviceMetricsOverride", {
    width: WIDTH, height: HEIGHT, deviceScaleFactor: 1, mobile: false,
  });
  await session("Page.navigate", { url });
  return {
    session,
    async eval(expression) {
      const r = await session("Runtime.evaluate", {
        expression, returnByValue: true, awaitPromise: true,
      });
      if (r.exceptionDetails) throw new Error(r.exceptionDetails.text + " " + expression);
      return r.result?.value;
    },
    async shot(path) {
      const s = await session("Page.captureScreenshot", { format: "png" });
      writeFileSync(path, Buffer.from(s.data, "base64"));
      return Buffer.from(s.data, "base64").length;
    },
    async close() {
      try {
        await cdp.send("Target.closeTarget", { targetId });
      } catch {}
    },
  };
}

// ---------------------------------------------------------------------------
// 1. gate: the badge points at the failing condition, and clicking marks it
// ---------------------------------------------------------------------------
// readGateBanner waits for the gate page to have answered, then returns the
// banner state. Waiting for the element is not enough: the unknown banner is
// also the pre-load placeholder, so an element-only wait passes on a page that
// has not read anything yet. Wait for it to leave that state.
async function readGateBanner(page) {
  await page.eval(`(async () => {
    for (let i = 0; i < 300; i++) {
      const el = document.querySelector('.gate-banner');
      if (el && !el.classList.contains('is-unknown')) return true;
      await new Promise(r => setTimeout(r, 100));
    }
    return false;
  })()`);
  const banner = await page.eval(
    `JSON.stringify({
       cls: document.querySelector('.gate-banner')?.className || '',
       text: document.querySelector('.gate-banner')?.innerText || '',
       btn: !!document.querySelector('[data-action="gate.locate"]'),
       target: document.querySelector('[data-action="gate.locate"]')?.dataset.target || '',
       reason: document.querySelector('.gate-reason')?.innerText || '',
     })`,
  );
  console.log("banner:", banner.replace(/\n/g, " | "));
  return JSON.parse(banner);
}

// checkPerLanguage is T2.2's whole point: each language is judged against its
// own files. A run-wide worst made every language print the same number, so the
// check is that two languages with real data disagree and a language with no
// files in this run is not silently borrowing another's measurement.
async function checkPerLanguage(page) {
  const perLang = await page.eval(`(async () => {
    const g = await (await fetch('/api/gate?scope=all')).json();
    const out = {};
    for (const c of g.conditions || []) {
      (out[c.language] ||= {})[c.measure] = { current: c.current, hasData: c.hasData };
    }
    return JSON.stringify(out);
  })()`);
  const langs = JSON.parse(perLang);
  console.log("per-language:", perLang);
  const go = langs.go || {}, js = langs.javascript || {};
  check("lang-go-has-real-values", (go["cyclomatic-complexity"]?.current ?? 0) > 0,
    `go cyclomatic = ${go["cyclomatic-complexity"]?.current}`);
  check("lang-js-differs-from-go",
    js["cyclomatic-complexity"]?.current !== go["cyclomatic-complexity"]?.current,
    `both reported ${go["cyclomatic-complexity"]?.current}`);
  check("lang-js-has-its-own-values", (js["file-length"]?.current ?? 0) > 0 &&
    js["file-length"]?.current !== go["file-length"]?.current,
    `js file-length = ${js["file-length"]?.current}, go = ${go["file-length"]?.current}`);
  if (langs.java) {
    const javaBorrowed = Object.values(langs.java).some((v) => v.current !== 0);
    check("lang-java-does-not-borrow", !javaBorrowed,
      `java reported ${JSON.stringify(langs.java)}`);
  }
}

async function checkGate(cdp) {
  const url = `${BASE}/#/view=gate&scope=${encodeURIComponent(REPO)}`;
  const page = await open(cdp, url);
  try {
    const b = await readGateBanner(page);
    check("gate-banner-failed", b.cls.includes("is-failed"), b.cls);
    check("gate-locate-present", b.btn, "no locate control");
    check("gate-locate-target", /^cond-/.test(b.target), b.target);

    // The target has to be a row the table actually rendered, otherwise the
    // button is a control that does nothing.
    const rowExists = await page.eval(
      `!!document.getElementById(${JSON.stringify(b.target)})`,
    );
    check("gate-target-row-exists", rowExists, b.target);

    const beforeShot = await page.shot(join(outdir, "gate-before-click.png"));

    // Scrolled to the bottom first: a jump that is already in view proves
    // nothing about scrolling, and the failing condition is the first row, so
    // an unscrolled click would land on a row that never had to move.
    await page.eval(`window.scrollTo(0, document.body.scrollHeight)`);
    const wasAway = await page.eval(`(async () => {
      const btn = document.querySelector('[data-action="gate.locate"]');
      const r = document.getElementById(btn.dataset.target).getBoundingClientRect();
      return r.bottom < 0 || r.top > window.innerHeight;
    })()`);
    check("gate-target-scrolled-out", wasAway, "the target stayed in view; scroll is unproven");

    // Click through the real handler, then let the smooth scroll settle.
    const after = await page.eval(`(async () => {
      const btn = document.querySelector('[data-action="gate.locate"]');
      btn.click();
      await new Promise(r => setTimeout(r, 1200));
      const row = document.getElementById(btn.dataset.target);
      const rect = row.getBoundingClientRect();
      return JSON.stringify({
        marked: row.className.includes('is-located'),
        inViewport: rect.top >= 0 && rect.bottom <= window.innerHeight,
        scrollY: Math.round(window.scrollY),
        top: Math.round(rect.top),
        text: row.innerText.replace(/\\s+/g, ' ').slice(0, 80),
      });
    })()`);
    const a = JSON.parse(after);
    console.log("after click:", after);
    check("gate-locate-marks-row", a.marked, "row has no is-located class");
    check("gate-locate-scrolls", a.inViewport && a.scrollY < 200, `row at top=${a.top} scrollY=${a.scrollY}`);
    const afterShot = await page.shot(join(outdir, "gate-after-click.png"));
    console.log(`shots gate-before-click.png=${beforeShot}B gate-after-click.png=${afterShot}B`);

    await checkPerLanguage(page);
    return a;
  } finally {
    await page.close();
  }
}

// ---------------------------------------------------------------------------
// 2. source window: a real finding renders real code lines
// ---------------------------------------------------------------------------
async function checkSource(cdp) {
  const url = `${BASE}/#/view=issues&scope=${encodeURIComponent(REPO)}`;
  const page = await open(cdp, url);
  try {
    // Select the first issue row that names a real source file, then open the
    // "where" tab — that is the operator's actual path to the window.
    const picked = await page.eval(`(async () => {
      for (let i = 0; i < 150; i++) {
        const row = document.querySelector('[data-action="issue.select"]');
        if (row) return JSON.stringify({ fp: row.dataset.fingerprint });
        await new Promise(r => setTimeout(r, 100));
      }
      return JSON.stringify({ fp: '' });
    })()`);
    const fp = JSON.parse(picked).fp;
    console.log("selected fingerprint:", fp);
    check("issues-row-present", !!fp, "no selectable issue row");
    if (!fp) return;

    await page.eval(`(async () => {
      document.querySelector('[data-action="issue.select"]').click();
      await new Promise(r => setTimeout(r, 400));
      const tab = document.querySelector('[data-action="issue.tab"][data-tab="where"]');
      if (tab) tab.click();
      for (let i = 0; i < 120; i++) {
        const w = document.querySelector('.source-window, .source-view, .issue-source');
        if (w && w.querySelector('.source-line, .source-no')) return true;
        await new Promise(r => setTimeout(r, 100));
      }
      return false;
    })()`);

    const src = await page.eval(`JSON.stringify({
       file: (document.querySelector('.source-file') || {}).innerText || '',
       numbers: [...document.querySelectorAll('.source-no')].map(e => e.innerText).slice(0, 5),
       rows: document.querySelectorAll('.source-line').length,
       anchors: document.querySelectorAll('.source-line.is-anchor').length,
       highlighted: document.querySelectorAll('.source-line[class*="hl-"]').length,
       body: (document.querySelector('.issue-detail') || {}).innerText || '',
     })`);
    console.log("source:", src.slice(0, 500));
    const s = JSON.parse(src);
    check("source-rows-rendered", s.rows > 0, `${s.rows} rows`);
    check("source-numbers-present", s.numbers.length > 0, "no line numbers");
    check("source-anchor-marked", s.anchors === 1, `${s.anchors} anchor lines`);
    const noReason = !/没有可读|不可读|no readable file path/.test(s.body);
    check("source-no-unreadable-reason", noReason, "the window reported an unreadable file");
    const shot = await page.shot(join(outdir, "source-window.png"));
    console.log(`shot source-window.png=${shot}B`);
    return s;
  } finally {
    await page.close();
  }
}

// ---------------------------------------------------------------------------
try {
  const deadline = Date.now() + 60000;
  let version = null;
  while (Date.now() < deadline) {
    try {
      const res = await fetch(`http://127.0.0.1:${PORT}/json/version`);
      const doc = await res.json();
      if (doc.webSocketDebuggerUrl) {
        version = doc;
        break;
      }
    } catch {}
    await sleep(250);
  }
  if (!version) throw new Error("browser never answered");
  console.log("browser up:", version.Browser);
  const cdp = await connect(version.webSocketDebuggerUrl);
  await checkGate(cdp);
  await checkSource(cdp);
  cdp.close();
  console.log(
    failures.length ? `\nFAILED ${failures.length}:\n  ` + failures.join("\n  ")
                    : "\nALL CHECKS PASSED",
  );
  shutdown();
  process.exit(failures.length ? 1 : 0);
} catch (err) {
  console.log("aborted:", err.message);
  shutdown();
  process.exit(4);
}
