// "Full view": the token-monitor dashboard (github.com/artur-faizov/AI-sessions-dashboard)
// in the browser. Its server runs visibly in a Herdr tab "token-monitor" (closing the tab
// stops it), started once and reused; NO_BROWSER keeps it from opening its own browser
// tab, and the URL comes from its "Token usage tree: <url>" line.
//
// The binary: `token-monitor` on PATH, else one downloaded earlier to
// ~/.cache/ai-sessions/, else the latest GitHub release's asset for this OS/arch,
// downloaded there with curl.

import { exec, hc, run, home } from "herdr";

const REPO = "artur-faizov/AI-sessions-dashboard";
const NAME = "token-monitor";
const LABEL = "herdr.token-monitor=1";
// Release asset names, as `make build-all` writes them.
const ASSETS = { "macos/aarch64": "dashboard-mac-darwin", "linux/x86_64": "dashboard-lin-x64",
  "linux/aarch64": "dashboard-lin-arm64", "windows/x86_64": "dashboard.exe" };

const win = () => process.platform === "windows";
const cacheDir = () => `${home()}/.cache/ai-sessions`;
const cached = () => `${cacheDir()}/${NAME}${win() ? ".exe" : ""}`;
const sleep = (s) => (win() ? exec("powershell", ["-NoProfile", "-Command", `Start-Sleep -Milliseconds ${s * 1000}`]) : exec("sleep", [String(s)]));

async function onPath() {
  try {
    const r = win() ? await exec("where", [NAME]) : await exec("sh", ["-c", `command -v ${NAME}`]);
    return r.code === 0 ? r.stdout.split(/\r?\n/)[0].trim() || null : null;
  } catch { return null; }
}

async function isFile(p) {
  try { return (win() ? await exec("cmd", ["/c", "if", "exist", p, "echo", "y"]) : await exec("test", ["-x", p])).code === 0; } catch { return false; }
}

// Downloads the latest release's asset for this machine into the cache dir.
async function download() {
  const key = `${process.platform}/${process.arch}`, asset = ASSETS[key];
  if (!asset) throw new Error(`No prebuilt token-monitor for ${key}. Build it from ${REPO} (make build) and put it on PATH as "${NAME}".`);
  const r = await fetch(`https://api.github.com/repos/${REPO}/releases/latest`, { headers: { Accept: "application/vnd.github+json", "User-Agent": "herdr-ai-sessions" } });
  if (r.status === 404) throw new Error(`${REPO} has no public release yet (or the repo is private). Build token-monitor from source (make build) and put it on PATH as "${NAME}".`);
  if (!r.ok) throw new Error(`GitHub releases: HTTP ${r.status}`);
  const rel = /** @type {any} */ (await r.json());
  const url = (rel.assets || []).find((a) => a.name === asset)?.browser_download_url;
  if (!url) throw new Error(`Release ${rel.tag_name} of ${REPO} has no ${asset}.`);
  const dest = cached(), part = `${dest}.part`;
  const curl = await exec(win() ? "curl.exe" : "curl", ["-fsSL", "--create-dirs", "-o", part, url], { timeout: 300000 });
  if (curl.code !== 0) throw new Error(`Download of ${asset} failed: ${curl.stderr.trim() || `curl exit ${curl.code}`}`);
  const mv = win() ? await exec("cmd", ["/c", "move", "/y", part.replace(/\//g, "\\"), dest.replace(/\//g, "\\")])
    : await exec("sh", ["-c", 'chmod +x "$1" && mv -f "$1" "$2"', "sh", part, dest]);
  if (mv.code !== 0) throw new Error(`Could not install ${dest}: ${mv.stderr.trim()}`);
  return dest;
}

async function binary(say) {
  const p = (await onPath()) || ((await isFile(cached())) ? cached() : null);
  if (p) return p;
  say(`Downloading token-monitor from ${REPO} releases…`);
  return download();
}

const ls = async (...a) => JSON.parse((await hc(["ls", "--json", ...a])).stdout || "{}").terminals || [];

/** Opens the dashboard on `pick` ({id, provider}) or its start page. `say` posts a progress note. */
export async function fullView(pick, say) {
  let addr = (await ls("--label", LABEL))[0]?.address, url = null;
  if (!addr) {
    const bin = await binary(say);
    // A new tab's terminal is the one in a tab named token-monitor that wasn't there before; label it.
    const before = new Set((await ls()).map((t) => t.address));
    const argv = win() ? ["cmd", "/c", `set NO_BROWSER=1&& "${bin}"`] : ["env", "NO_BROWSER=1", bin];
    run("tab.new", { name: NAME, argv });
    for (let i = 0; i < 20 && !addr; i++) {
      addr = (await ls()).find((t) => !before.has(t.address) && String(t.where || "").endsWith(`/ ${NAME}`))?.address;
      if (!addr) await sleep(0.25);
    }
    if (addr) await hc(["label", addr, LABEL]);
  }
  for (let i = 0; addr && i < 40 && !url; i++) {  // ≤ 10 s
    url = ((await hc(["read", addr, "--lines", "50"])).stdout.match(/Token usage tree: (http:\/\/localhost:\d+)/) || [])[1];
    if (!url) await sleep(0.25);
  }
  if (!url) throw new Error("token-monitor did not report its URL within 10 s (see its tab)");
  // token-monitor's ids: "<provider>:<own id>"; a Claude subagent is "agent-<file id>", a Codex child its thread id.
  const id = pick && `${pick.provider === "codex" ? "codex" : "claude"}:${pick.id}`;
  const target = id ? `${url}/#session=${encodeURIComponent(id)}` : url;
  if (win()) await exec("cmd", ["/c", "start", "", target]);
  else await exec(process.platform === "macos" ? "open" : "xdg-open", [target]);
}
