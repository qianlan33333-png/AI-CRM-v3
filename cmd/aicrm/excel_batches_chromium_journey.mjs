import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawn, spawnSync } from "node:child_process";
import {
  chromiumStartupDiagnostic,
  chromiumStartupTimeoutMS,
} from "../../internal/webshell/chromium_launch.mjs";

const baseURL = process.env.AICRM_EXCEL_TEST_URL;
const username = process.env.AICRM_EXCEL_TEST_USERNAME;
const password = process.env.AICRM_EXCEL_TEST_PASSWORD;
if (!/^https:\/\//.test(baseURL || "") || !username || !password)
  throw new Error(
    "Excel batch Chromium journey requires HTTPS URL and test credentials",
  );
const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const candidates = () => {
  const explicit = [
    process.env.AICRM_CHROMIUM_BINARY,
    process.env.CHROME_BIN,
  ].filter(Boolean);
  if (process.platform === "darwin")
    explicit.push(
      "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
    );
  return [
    ...explicit,
    "google-chrome",
    "google-chrome-stable",
    "chromium",
    "chromium-browser",
  ];
};
function browserBinary() {
  for (const candidate of candidates()) {
    if (candidate.includes("/")) {
      try {
        if (
          spawnSync(candidate, ["--version"], { stdio: "ignore" }).status === 0
        )
          return candidate;
      } catch (_) {}
    } else if (
      spawnSync("which", [candidate], { stdio: "ignore" }).status === 0
    )
      return candidate;
  }
  throw new Error("Chromium binary is unavailable");
}
class CDP {
  constructor(socket) {
    this.socket = socket;
    this.nextID = 0;
    this.pending = new Map();
    this.events = new Map();
    socket.addEventListener("message", (event) => {
      const message = JSON.parse(String(event.data));
      if (message.id && this.pending.has(message.id)) {
        const pending = this.pending.get(message.id);
        this.pending.delete(message.id);
        message.error
          ? pending.reject(new Error(`CDP ${message.error.code || "error"}`))
          : pending.resolve(message.result || {});
        return;
      }
      for (const listener of this.events.get(message.method) || [])
        listener(message.params || {});
    });
  }
  call(method, params = {}) {
    return new Promise((resolve, reject) => {
      const id = ++this.nextID;
      this.pending.set(id, { resolve, reject });
      this.socket.send(JSON.stringify({ id, method, params }));
    });
  }
  on(method, listener) {
    const list = this.events.get(method) || [];
    list.push(listener);
    this.events.set(method, list);
  }
  close() {
    for (const { reject } of this.pending.values())
      reject(new Error("CDP browser closed"));
    this.pending.clear();
    this.socket.close();
  }
}
async function port(profile) {
  const deadline = Date.now() + chromiumStartupTimeoutMS;
  while (Date.now() < deadline) {
    try {
      const value = String(
        await fs.readFile(path.join(profile, "DevToolsActivePort"), "utf8"),
      ).split("\n")[0];
      if (/^\d+$/.test(value)) return `http://127.0.0.1:${value}`;
    } catch {}
    if (browser?.exitCode !== null || browser?.signalCode) break;
    await delay(50);
  }
  throw new Error(
    chromiumStartupDiagnostic({
      profile,
      exitCode: browser?.exitCode,
      signalCode: browser?.signalCode,
      stderr: browserStderr,
    }),
  );
}
async function evaluate(cdp, expression) {
  const result = await cdp.call("Runtime.evaluate", {
    expression,
    returnByValue: true,
    awaitPromise: true,
  });
  if (result.exceptionDetails) throw new Error("page evaluation failed");
  return result.result?.value;
}
async function waitFor(cdp, expression, message) {
  for (let i = 0; i < 180; i += 1) {
    if (await evaluate(cdp, expression)) return;
    await delay(50);
  }
  throw new Error(
    message +
      " " +
      JSON.stringify(
        await evaluate(
          cdp,
          `({path:location.pathname,status:document.querySelector(".operation-excel-workspace [role=status]")?.textContent,root:document.querySelector(".operation-excel-workspace")?.textContent?.slice(0,1000)})`,
        ),
      ),
  );
}
async function browserExit(child) {
  if (!child || child.exitCode !== null || child.signalCode !== null) return;
  await Promise.race([
    new Promise((resolve) => child.once("exit", resolve)),
    delay(3000),
  ]);
  if (child.exitCode === null && child.signalCode === null) {
    child.kill("SIGKILL");
    await Promise.race([
      new Promise((resolve) => child.once("exit", resolve)),
      delay(1000),
    ]);
  }
}
async function removeProfile(profile) {
  for (let i = 0; i < 40; i += 1) {
    try {
      await fs.rm(profile, { recursive: true, force: true, maxRetries: 0 });
      return true;
    } catch (error) {
      if (!["ENOTEMPTY", "EBUSY", "EPERM"].includes(error?.code)) return false;
      await delay(100);
    }
  }
  return false;
}
const profile = await fs.mkdtemp(
  path.join(os.tmpdir(), "aicrm-excel-batches-chromium-"),
);
let browser;
let cdp;
let failed = false;
let browserStderr = "";
try {
  browser = spawn(
    browserBinary(),
    [
      "--headless=new",
      "--no-sandbox",
      "--remote-debugging-port=0",
      `--user-data-dir=${profile}`,
      "--no-first-run",
      "--no-default-browser-check",
      "--disable-background-networking",
      "--disable-component-update",
      "--disable-sync",
      "--ignore-certificate-errors",
      "--allow-insecure-localhost",
      "about:blank",
    ],
    { stdio: ["ignore", "ignore", "pipe"] },
  );
  browser.stderr.on("data", (chunk) => {
    browserStderr = (browserStderr + chunk.toString()).slice(-2048);
  });
  const created = await (
    await fetch(`${await port(profile)}/json/new?about:blank`, {
      method: "PUT",
    })
  ).json();
  const socket = new WebSocket(created.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => {
    socket.addEventListener("open", resolve, { once: true });
    socket.addEventListener(
      "error",
      () => reject(new Error("Chromium page connection failed")),
      { once: true },
    );
  });
  cdp = new CDP(socket);
  await cdp.call("Page.enable");
  await cdp.call("Runtime.enable");
  await cdp.call("Network.enable");

  const errors = [];
  cdp.on("Runtime.exceptionThrown", () => errors.push("page_exception"));
  await cdp.call("Page.navigate", {
    url: `${baseURL}/login?next=%2Fadmin%2Foperation-cycles`,
  });
  await waitFor(
    cdp,
    `Boolean(document.querySelector('form[action="/login"] input[name="login_csrf_token"]'))`,
    "login did not render",
  );
  await evaluate(
    cdp,
    `(()=>{document.querySelector('input[name="username"]').value=${JSON.stringify(username)};document.querySelector('input[name="password"]').value=${JSON.stringify(password)};document.querySelector('form[action="/login"]').requestSubmit();return true})()`,
  );
  await waitFor(
    cdp,
    `Boolean(document.querySelector('.operation-excel-workspace button')&&[...document.querySelectorAll('.operation-excel-workspace button')].find(b=>b.textContent==='查看详情'))`,
    "operation plan list missing",
  );
  await evaluate(
    cdp,
    `[...document.querySelectorAll('.operation-excel-workspace button')].find(b=>b.textContent==='查看详情').click();true`,
  );
  await waitFor(
    cdp,
    `Boolean(document.querySelector('.xeb-detail-main')&&[...document.querySelectorAll('.xeb-detail-main button')].find(b=>b.textContent==='新建发送批次'))`,
    "operation detail did not open",
  );
  await evaluate(
    cdp,
    `[...document.querySelectorAll('.xeb-detail-main button')].find(b=>b.textContent==='新建发送批次').click();true`,
  );
  await waitFor(
    cdp,
    `Boolean(document.querySelector('dialog[open] input[type=file]'))`,
    "new batch dialog missing",
  );
  await evaluate(
    cdp,
    `(()=>{const input=document.querySelector('dialog[open] input[type=file]');const transfer=new DataTransfer();transfer.items.add(new File(['fixture'],'batch.xlsx'));input.files=transfer.files;input.dispatchEvent(new Event('change'));const fresh=document.querySelector('dialog[open] input[type=checkbox]');fresh.checked=true;fresh.dispatchEvent(new Event('change'));[...document.querySelectorAll('dialog[open] button')].find(b=>b.textContent==='上传并开始审核').click();return true})()`,
  );
  await waitFor(
    cdp,
    `!document.querySelector('dialog[open]')&&document.querySelector('.xeb-detail-main')?.textContent.includes('当前批次 #2')&&document.querySelector('.xeb-detail-main')?.textContent.includes('第一条待审核话术')`,
    "controlled Excel upload did not open the selected batch",
  );
  if (
    !(await evaluate(
      cdp,
      `[...document.querySelectorAll('.xeb-detail-main button')].find(b=>b.textContent==='审核通过并创建企微群发任务').disabled`,
    ))
  )
    throw new Error("cover required guard missing");
  await evaluate(
    cdp,
    `(()=>{const input=document.querySelector('input[aria-label="统一封面图片"]');const dt=new DataTransfer();dt.items.add(new File([Uint8Array.from(atob('iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAYAAABytg0kAAAAFElEQVR4nGL6z8DwnwEZAAIAAP//HxcCAa7PZcoAAAAASUVORK5CYII='),c=>c.charCodeAt(0))],'cover.png',{type:'image/png'}));input.files=dt.files;[...document.querySelectorAll('.xeb-detail-main button')].find(b=>b.textContent==='上传统一封面').click();return true})()`,
  );
  await waitFor(
    cdp,
    `document.querySelector('[role=status]')?.textContent.includes('统一封面已更新')`,
    "cover upload failed",
  );
  await evaluate(
    cdp,
    `[...document.querySelectorAll('.xeb-detail-main button')].find(b=>b.textContent==='修改').click();true`,
  );
  await waitFor(
    cdp,
    `Boolean(document.querySelector('dialog[open] textarea'))`,
    "edit dialog missing",
  );
  await evaluate(
    cdp,
    `document.querySelector('dialog textarea').value='人工修改后的话术';[...document.querySelectorAll('dialog button')].find(b=>b.textContent==='保存并重新审核').click();true`,
  );
  await waitFor(
    cdp,
    `!document.querySelector('dialog[open]')&&document.querySelector('.xeb-detail-main')?.textContent.includes('人工修改后的话术')`,
    "edited wording did not persist",
  );
  await evaluate(
    cdp,
    `[...document.querySelectorAll('.xeb-detail-main button')].filter(b=>b.textContent==='排除')[1].click();true`,
  );
  await waitFor(
    cdp,
    `document.querySelector('.xeb-detail-main')?.textContent.includes('已排除1')`,
    "excluded row stayed eligible",
  );
  await evaluate(
    cdp,
    `[...document.querySelectorAll('.xeb-detail-main button')].find(b=>b.textContent==='审核通过并创建企微群发任务').click();true`,
  );
  await waitFor(
    cdp,
    `document.querySelector('[role=status]')?.textContent.includes('企微任务意图已创建')&&!([...document.querySelectorAll('.xeb-detail-main button')].some(b=>b.textContent==='审核通过并创建企微群发任务'))`,
    "single approval did not queue one target",
  );
  await evaluate(
    cdp,
    `document.querySelector('.xeb-detail-nav button[data-tab="effects"]').click();true`,
  );
  await waitFor(
    cdp,
    `document.querySelector('.xeb-detail-main')?.textContent.includes('最近采集：2026-09-07 09:02:03')&&!document.querySelector('.xeb-detail-main')?.textContent.includes('2026-09-07T01:02:03')`,
    "report timestamp was not rendered as a Shanghai whole-second value",
  );
  await evaluate(
    cdp,
    `document.querySelector('.xeb-detail-nav button[data-tab="content"]').click();true`,
  );
  await waitFor(
    cdp,
    `Boolean([...document.querySelectorAll('.xeb-detail-main button')].find(b=>b.textContent==='查看旧版本'))`,
    "content tab did not restore the historical-version entry",
  );
  await evaluate(
    cdp,
    `[...document.querySelectorAll('.xeb-detail-main button')].find(b=>b.textContent==='查看旧版本').click();true`,
  );
  await waitFor(
    cdp,
    `/\\d{4}-\\d{2}-\\d{2} \\d{2}:\\d{2}:\\d{2}/.test(document.querySelector('dialog[open]')?.textContent||'')&&!/\\d{4}-\\d{2}-\\d{2}T\\d{2}:\\d{2}:\\d{2}/.test(document.querySelector('dialog[open]')?.textContent||'')`,
    "historical-version timestamp was not rendered as a Shanghai whole-second value",
  );
  await evaluate(cdp, `document.querySelector('dialog[open] button')?.click();true`);
  await cdp.call("Page.reload");
  await waitFor(
    cdp,
    `document.querySelector('.xeb-detail-main')?.textContent.includes('人工修改后的话术')&&!([...document.querySelectorAll('.xeb-detail-main button')].some(b=>b.textContent==='审核通过并创建企微群发任务'))`,
    "state did not survive reload",
  );
  if (errors.length) throw new Error("runtime exceptions in Excel Host");
  console.log("excel_batches_chromium: PASS");
} catch (error) {
  failed = true;
  throw error;
} finally {
  if (cdp) cdp.close();
  if (browser && browser.exitCode === null && browser.signalCode === null) {
    browser.kill("SIGTERM");
    await browserExit(browser);
  }
  await removeProfile(profile);
}
