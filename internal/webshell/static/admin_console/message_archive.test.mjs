import { JSDOM } from "jsdom";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const script = fs.readFileSync(path.join(here, "message_archive.js"), "utf8");
const wait = () => new Promise((resolve) => setTimeout(resolve, 20));
const html = `<!doctype html><section data-message-archive-root data-api-base="/api/admin/message-archive/customers/"><form id="archive-search-form"><input name="q"><input name="start_at"><input name="end_at"><select name="chat_type"></select><select name="message_type"></select><select name="direction"></select><input name="staff_user_id"></form><div id="archive-state"></div><ol id="archive-message-list"></ol><button id="archive-more"></button></section>`;

function response(body, type = "application/json", status = 200) {
  return { ok: status >= 200 && status < 300, status, headers: new Headers({ "Content-Type": type }), json: async () => body, blob: async () => new Blob([body], { type }) };
}
function page(mediaType) {
  const dom = new JSDOM(html, { url: "https://test.invalid/admin/message-archive/customers/1", runScripts: "outside-only", pretendToBeVisual: true });
  const revoked = [];
  dom.window.URL.createObjectURL = () => "blob:private-image";
  dom.window.URL.revokeObjectURL = (value) => revoked.push(value);
  dom.window.fetch = async (input) => {
    const url = new URL(String(input), dom.window.location.origin);
    if (url.pathname.endsWith("/media/7")) return response(new Uint8Array([137, 80, 78, 71]), mediaType);
    return response({ items: [{ id: 1, occurred_at: "2026-09-05T00:00:00Z", chat_type: "private", message_type: "image", render_type: "supported", direction: "customer_to_staff", staff_names: ["员工"], media_ids: [7] }], next: null });
  };
  dom.window.eval(script);
  return { dom, revoked };
}

const good = page("image/png");
await wait();
const button = good.dom.window.document.querySelector(".archive-media");
if (!button) throw new Error("archive page did not render the protected image control");
button.click();
await wait();
if (!(good.dom.window.document.querySelector(".archive-media__image")?.src === "blob:private-image")) throw new Error("recognized image response did not render a DOM image");
good.dom.window.dispatchEvent(new good.dom.window.Event("pagehide"));
if (!good.revoked.includes("blob:private-image")) throw new Error("private image object URL was not released");
good.dom.window.close();

const bad = page("text/html");
await wait();
bad.dom.window.document.querySelector(".archive-media").click();
await wait();
if (bad.dom.window.document.querySelector(".archive-media__image") || bad.dom.window.document.querySelector(".archive-media")?.textContent !== "图片暂不可读取") throw new Error("unsupported media response rendered as an image");
bad.dom.window.close();
console.log("message-archive-browser: PASS");
