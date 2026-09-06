import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawn, spawnSync } from "node:child_process";

const baseURL = process.env.AICRM_RUNTIME_RELEASE_TEST_URL;
const username = process.env.AICRM_RUNTIME_RELEASE_TEST_USERNAME;
const password = process.env.AICRM_RUNTIME_RELEASE_TEST_PASSWORD;
if (!/^https:\/\//.test(baseURL || "") || !username || !password) {
  throw new Error("runtime release Chromium journey requires HTTPS URL and test login credentials");
}

const delay = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));
const chromeCandidates = () => {
  const explicit = [process.env.AICRM_CHROMIUM_BINARY, process.env.CHROME_BIN].filter(Boolean);
  if (process.platform === "darwin") explicit.push("/Applications/Google Chrome.app/Contents/MacOS/Google Chrome");
  return [...explicit, "google-chrome", "google-chrome-stable", "chromium", "chromium-browser"];
};
const browserBinary = () => {
  for (const candidate of chromeCandidates()) {
    if (candidate.includes("/")) {
      try { if (spawnSync(candidate, ["--version"], { stdio: "ignore" }).status === 0) return candidate; } catch (_) {}
      continue;
    }
    if (spawnSync("which", [candidate], { stdio: "ignore" }).status === 0) return candidate;
  }
  throw new Error("Chromium binary is unavailable");
};

class CDP {
  constructor(socket) {
    this.socket = socket;
    this.nextID = 0;
    this.pending = new Map();
    socket.addEventListener("message", (event) => {
      const message = JSON.parse(String(event.data));
      if (!message.id || !this.pending.has(message.id)) return;
      const { resolve, reject } = this.pending.get(message.id);
      this.pending.delete(message.id);
      if (message.error) reject(new Error(`CDP ${message.error.code || "error"}`));
      else resolve(message.result || {});
    });
  }
  call(method, params = {}) {
    return new Promise((resolve, reject) => {
      const id = ++this.nextID;
      this.pending.set(id, { resolve, reject });
      this.socket.send(JSON.stringify({ id, method, params }));
    });
  }
  close() {
    for (const { reject } of this.pending.values()) reject(new Error("CDP browser closed"));
    this.pending.clear();
    this.socket.close();
  }
}


const waitForPort = async (profile) => {
  const activePort = path.join(profile, "DevToolsActivePort");
  for (let attempt = 0; attempt < 160; attempt += 1) {
    try {
      const [port] = String(await fs.readFile(activePort, "utf8")).split("\n");
      if (/^\d+$/.test(port)) return `http://127.0.0.1:${port}`;
    } catch (_) {}
    await delay(50);
  }
  throw new Error("Chromium remote debugging did not become ready");
};

const waitFor = async (cdp, expression, message) => {
  for (let attempt = 0; attempt < 160; attempt += 1) {
    const result = await cdp.call("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true });
    if (result.result?.value) return;
    await delay(50);
  }
  throw new Error(message);
};

const evaluate = async (cdp, expression) => {
  const result = await cdp.call("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true });
  if (result.exceptionDetails) throw new Error("page evaluation failed");
  return result.result?.value;
};

const profile = await fs.mkdtemp(path.join(os.tmpdir(), "aicrm-runtime-config-chromium-"));
let browser;
let cdp;
try {
  const binary = browserBinary();
  browser = spawn(binary, [
    "--headless=new", "--no-sandbox", "--remote-debugging-port=0", `--user-data-dir=${profile}`,
    "--no-first-run", "--no-default-browser-check", "--disable-background-networking",
    "--disable-component-update", "--disable-sync", "--ignore-certificate-errors", "--allow-insecure-localhost",
    "about:blank",
  ], { stdio: ["ignore", "ignore", "ignore"] });
  const browserAddress = await waitForPort(profile);
  const created = await (await fetch(`${browserAddress}/json/new?about:blank`, { method: "PUT" })).json();
  const socket = new WebSocket(created.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => {
    socket.addEventListener("open", resolve, { once: true });
    socket.addEventListener("error", () => reject(new Error("Chromium page connection failed")), { once: true });
  });
  cdp = new CDP(socket);
  await cdp.call("Page.enable");
  await cdp.call("Runtime.enable");

  await cdp.call("Page.navigate", { url: `${baseURL}/login?next=%2Fadmin%2Fconfig%2Freleases%2Fnew` });
  await waitFor(cdp, "Boolean(document.querySelector('form[action=\"/login\"] input[name=\"login_csrf_token\"]'))", "login shell did not render");
  await evaluate(cdp, `(() => {
    document.querySelector('input[name="username"]').value = ${JSON.stringify(username)};
    document.querySelector('input[name="password"]').value = ${JSON.stringify(password)};
    document.querySelector('form[action="/login"]').requestSubmit();
    return true;
  })()`);

  const publish = async (limit, ordinal) => {
    await waitFor(cdp, "Boolean(document.querySelector('[data-runtime-release-create] button[type=submit]:not([disabled])'))", `authenticated Config shell and Host did not render release ${ordinal}`);
    await evaluate(cdp, `(() => {
      const form = document.querySelector('[data-runtime-release-create]');
      form.querySelector('[name="max_recipients"]').value = ${JSON.stringify(String(limit))};
      form.querySelector('[name="confirm"]').checked = true;
      form.requestSubmit();
      return true;
    })()`);
    await waitFor(cdp, "Boolean(document.querySelector('[data-runtime-release-validate]'))", `draft ${ordinal} was not persisted through actual HTTP API`);
    await evaluate(cdp, "document.querySelector('[data-runtime-release-validate]').click(); true");
    await waitFor(cdp, "Boolean(document.querySelector('[data-runtime-release-publish]'))", `validation ${ordinal} was not persisted through actual HTTP API`);
    await evaluate(cdp, "document.querySelector('[data-runtime-release-publish]').click(); true");
    await waitFor(cdp, "Boolean(document.querySelector('[data-runtime-release-rollback]'))", `publication ${ordinal} was not persisted through actual HTTP API`);
  };

  await publish(2, 1);
  await cdp.call("Page.navigate", { url: `${baseURL}/admin/config/releases/new` });
  await publish(3, 2);
  await cdp.call("Page.navigate", { url: `${baseURL}/admin/config/releases/1` });
  await waitFor(cdp, "document.body.textContent.includes('用此版本回滚')", "superseded release did not expose the explicit rollback action");
  await evaluate(cdp, "document.querySelector('[data-runtime-release-rollback]').click(); true");
  await waitFor(cdp, "document.body.textContent.includes('已创建并发布回滚记录') && document.body.textContent.includes('实际使用回读')", "rollback and actual-use readback were not rendered");
  const effective = await evaluate(cdp, "fetch('/api/admin/config/runtime-releases', {credentials:'same-origin'}).then((response) => response.ok ? response.json() : null).then((body) => body?.runtime_releases?.effective?.automation_max_recipients_per_run)");
  if (effective !== 2) throw new Error("rollback did not restore the older runtime value through the actual API");
  console.log("runtime_config_releases_chromium: PASS");
} finally {
  if (cdp) cdp.close();
  if (browser && !browser.killed) browser.kill("SIGTERM");
  await fs.rm(profile, { recursive: true, force: true });
}
