#!/usr/bin/env node
// Builds the only runnable sidebar renderer from the immutable dd8 source.
// Each mutation below is an audited adapter boundary: chat dispatch is removed,
// and identity/request/SDK execution is delegated to the V3 trusted Host.
import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { spawnSync } from "node:child_process";

const root = path.resolve(path.dirname(new URL(import.meta.url).pathname), "..");
const source = path.join(root, "internal/webshell/static/sidebar_workbench/sidebar_workbench_dd8d60d.js");
const target = path.join(root, "web/dist/sidebar/sidebar_workbench_v3_overlay.js");
let js = fs.readFileSync(source, "utf8");
const want = "f20515f3192f3a11048929c7c7b375e1ae274165ae173c0d8415735ffa25424d";
if (crypto.createHash("sha256").update(js).digest("hex") !== want) throw new Error("dd8 sidebar JS source digest mismatch");
const once = (fragment, label) => {
  if (js.split(fragment).length !== 2) throw new Error(`${label} anchor mismatch`);
};
const replaceRange = (begin, finish, replacement, label) => {
  once(begin, `${label} begin`); once(finish, `${label} finish`);
  const from = js.indexOf(begin), to = js.indexOf(finish, from);
  if (to < from) throw new Error(`${label} range mismatch`);
  js = js.slice(0, from) + replacement + js.slice(to);
};
for (const fragment of [
  '    sidebar_owner_token: "",\n',
  '    sidebar_owner_token_external_userid: "",\n',
  '    sidebar_owner_token_status: "",\n',
  '    sidebar_oauth_url: "",\n',
  '    sidebar_oauth_started: false,\n',
]) { once(fragment, "retired context state"); js = js.replace(fragment, ""); }

for (const anchor of ["function renderProfile()", "function renderQuestionnaires()", "function renderProducts()", "function renderOrders()", "function renderCoupons()", "function renderMaterials()", "function renderRadarLinks(controls)"]) once(anchor, "donor render");

// S02: remove all archive/chat presentation and dispatch.  This is deliberately
// a source-range removal, not CSS hiding, so no chat request can be reached.
for (const fragment of ["    [\"other_staff_messages\", \"其他客服聊天\"],\n", "    other_staff_messages: 9000,\n", "      other_staff_messages: null,\n"]) { once(fragment, "chat removal"); js = js.replace(fragment, ""); }
replaceRange("  function renderOtherStaffMessages() {", "  function renderOwnerPendingWorkbench(message) {", "", "chat renderer");
replaceRange("    } else if (tab === \"other_staff_messages\") {", "  async function loadOrders(type) {", "    }\n    state.loaded[tab] = true;\n  }\n\n", "chat loader");
once("    if (state.activeTab === \"other_staff_messages\") renderOtherStaffMessages();\n", "chat active renderer");
js = js.replace("    if (state.activeTab === \"other_staff_messages\") renderOtherStaffMessages();\n", "");

// The donor request implementation carried retired owner-token, OAuth and
// JSSDK state. Keep only UI helpers; the bridge owns all identity and scoped
// request execution so legacy query/hash/session recovery cannot reappear.
replaceRange("  function safeJsonParse(text) {", "  function queryUrl(baseUrl, params) {", `  function writeDebug(label, payload) {
    if (!debugEnabled) return;
    const line = "[" + new Date().toISOString() + "] " + label + (payload === undefined ? "" : " " + JSON.stringify(payload));
    const item = document.createElement("pre");
    item.textContent = line;
    debugWrap.appendChild(item);
  }

  function customerContextQuery() {
    return {};
  }

  function productContextDiagnostics() {
    return { context_source: "v3_trusted_bridge", context_status: "scoped" };
  }

  function setExternalUserid(value) {
    state.external_userid = String(value || "").trim();
    return state.external_userid;
  }

  function showToast(message, tone) {
    window.clearTimeout(state.toastTimer);
    toastNode.textContent = message || "";
    toastNode.className = "toast" + (tone === "error" ? " error" : "");
    toastNode.classList.remove("hidden");
    state.toastTimer = window.setTimeout(() => toastNode.classList.add("hidden"), 1900);
  }

  async function requestJson(url, options) {
    const bridge = window.__AICRMSidebarBridge;
    if (!bridge) throw new Error("侧边栏可信桥未就绪");
    return bridge.request(url, options || {});
  }

`, "trusted bridge ownership");

// The V3 bridge performs the exact current JSSDK/OAuth/contact sequence. The
// donor remains responsible only for standard UI state and rendering.
replaceRange("  async function resolveContextFromQuery() {", "  tabsNode.addEventListener(\"click\", (event) => {", `  async function boot() {
    setWorkbenchState(WORKBENCH_STATES.identifying_customer);
    renderTabs();
    setPanelLoading("");
    try {
      const bridge = window.__AICRMSidebarBridge;
      if (!bridge) throw new Error("侧边栏可信桥未就绪");
      await bridge.start();
      // The overlay never receives or renders an external identifier. This
      // stable local marker keeps its donor cache keys isolated per reload.
      setExternalUserid("v3-trusted-context");
      await loadWorkbench();
    } catch (error) {
      setWorkbenchState(WORKBENCH_STATES.error, { message: error.message || String(error) });
      renderRetryPanel("", error.message || "加载失败，请稍后重试。");
    }
  }

`, "trusted boot");

// S07: no direct provider execution from the donor.  Product/material clicks
// go through V3 Outbound receipts, then the trusted bridge invokes JSSDK and
// records the outcome against that single accepted intent.
replaceRange("  async function sendMaterial(materialId) {", "  function fallbackCopyText(value) {", `  async function sendMaterial(materialId) {
    if (state.materialType !== "image") {
      showToast("雷达链接请使用复制链接", "error");
      return;
    }
    try {
      await window.__AICRMSidebarBridge.send({ resource_kind: "material", resource_id: String(materialId) });
      showToast("已发送到当前会话");
    } catch (error) {
      showToast(error.message || "发送失败", "error");
    }
  }

`, "material send bridge");
replaceRange("  async function sendProduct(productIndex, kind) {", "  function assertWeComSendOk(res) {", `  async function sendProduct(productIndex, kind) {
    const rows = kind === "service_period" ? state.data.service_period_products || [] : state.data.products || [];
    const item = rows[Number(productIndex)] || {};
    if (!item.id) {
      showToast("商品不可用", "error");
      return;
    }
    try {
      await window.__AICRMSidebarBridge.send({ resource_kind: "product", resource_id: String(item.id), product_type: kind === "service_period" ? "service_period" : "standard" });
      showToast("已发送商品");
    } catch (error) {
      showToast(error.message || "发送失败", "error");
    }
  }

`, "product send bridge");
replaceRange("  function assertWeComSendOk(res) {", "  function openMobileModal() {", "", "direct sdk send");

// The directory stays visible when a rule is scheduled, ended, sold out, or
// this customer has reached a personal limit. These display states do not
// replace the public claim handler's final transactional eligibility check.
replaceRange("  function renderCoupons() {", "  function materialTypeControls() {", `  function renderCoupons() {
    const rows = state.data.coupons || [];
    if (!rows.length) {
      content.innerHTML = panel("", empty("暂无可领取优惠券"));
      return;
    }
    const availabilityLabel = (item) => {
      if (item.user_limit_reached) return "已达到个人领取上限";
      return { active: "可前往领取页确认", scheduled: "未到领取时间", ended: "领取已截止", sold_out: "已领完" }[String(item.availability_status || "")] || "当前不可领取";
    };
    content.innerHTML = panel("", rows.map((item) => {
      const products = (item.products || []).map((product) => product.title || "").filter(Boolean).join("、");
      const unavailable = Boolean(item.user_limit_reached) || String(item.availability_status || "") !== "active" || !item.url;
      return '<article class="card link-card"><div class="card-title"><div><h3>' + escapeHtml(item.name || "未命名优惠券") + '</h3><div class="mini">' +
        escapeHtml(item.discount_label || "") + '</div></div></div><div class="kv"><span>适用商品</span><strong>' + escapeHtml(products || "全部已配置商品") +
        '</strong><span>领取截止</span><strong>' + escapeHtml(item.claim_ends_at || "") + '</strong><span>状态</span><strong>' + escapeHtml(availabilityLabel(item)) +
        '</strong></div><div class="row-actions"><button class="btn primary" type="button" data-copy-url="' + escapeHtml(item.url || "") + '"' + (unavailable ? " disabled" : "") + '>复制链接</button></div></article>';
    }).join(""));
  }

`, "coupon availability render");

// The release template intentionally omits this raw identifier field.
for (const fragment of [
  '    document.getElementById("customer-external-userid").textContent = state.external_userid ? "外部联系人 ID " + state.external_userid : "";\n',
  '    document.getElementById("customer-external-userid").textContent = externalUserid ? "外部联系人 ID " + externalUserid : "";\n',
]) { once(fragment, "external identifier render"); js = js.replace(fragment, ""); }

for (const forbidden of ["other_staff_messages", "其他客服聊天", "chat_activity", "other-staff-messages", "/api/sidebar/v2/other-staff-messages"]) {
  if (js.includes(forbidden)) throw new Error(`removed chat capability survived overlay: ${forbidden}`);
}
fs.mkdirSync(path.dirname(target), { recursive: true });
fs.writeFileSync(target, js);
const checked = spawnSync(process.execPath, ["--check", target], { encoding: "utf8" });
if (checked.status !== 0) throw new Error(`generated overlay syntax invalid: ${checked.stderr || checked.stdout}`);
console.log(JSON.stringify({ source_sha256: want, target, chat_dispatch_removed: true, trusted_bridge: true }));
