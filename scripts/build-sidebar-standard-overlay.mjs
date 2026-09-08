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
replaceRange("  async function resolveContextFromQuery() {", "  tabsNode.addEventListener(\"click\", (event) => {", `  async function boot(options) {
    // A retry or a new visible WebView context supersedes every prior boot.
    // The previous contact/JSSDK promise may still settle after cancellation;
    // it must never redraw an error or a workbench over the newer context.
    const generation = Number(state.v3TrustedBootGeneration || 0) + 1;
    state.v3TrustedBootGeneration = generation;
    setWorkbenchState(WORKBENCH_STATES.identifying_customer);
    renderTabs();
    setPanelLoading("");
    try {
      const bridge = window.__AICRMSidebarBridge;
      if (!bridge) throw new Error("侧边栏可信桥未就绪");
      // A retry explicitly abandons the prior generation before resolving a
      // new WeCom contact. It cannot reuse a token/cache from the old view.
      if (options && options.forceSidebarOAuth && typeof bridge.retry === "function") await bridge.retry();
      else await bridge.start();
      if (state.v3TrustedBootGeneration !== generation) return;
      // The overlay never receives or renders an external identifier. This
      // stable local marker keeps its donor cache keys isolated per reload.
      setExternalUserid("v3-trusted-context");
      await loadWorkbench();
    } catch (error) {
      if (state.v3TrustedBootGeneration !== generation) return;
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

// The donor used a single generic "bound" mobile label and made regular-order
// detail buttons unconditionally clickable. V3 keeps the standard layout but
// only presents facts supplied by the owning projection.
replaceRange("  function renderTop() {", "  function updateProfileField(key, value) {", `  function renderTop() {
    const workbench = state.workbench || {};
    const customer = workbench.customer || {};
    const workflow = workbench.workflow || {};
    const name = String(customer.display_name || "当前客户").trim();
    const mobile = String(customer.mobile || "").trim();
    const assurance = String(customer.phone_assurance || "").toLowerCase();
    const isVerified = assurance === "verified";
    document.getElementById("customer-name").textContent = name;
    document.getElementById("customer-mobile").textContent = mobile ? "手机号 " + mobile : "";
    document.getElementById("workflow-title").textContent = String(workflow.title || "").trim();
    const bindingState = document.getElementById("binding-state");
    const hideBindingState = Boolean(customer.owner_pending);
    bindingState.textContent = hideBindingState ? "" : (mobile ? (isVerified ? "手机号已验证" : "手机号已声明") : "手机号未声明");
    bindingState.classList.toggle("hidden", hideBindingState);
    bindingState.classList.remove("loading");
    bindingState.classList.toggle("unbound", !mobile);
    const changeButton = document.getElementById("change-mobile-button");
    if (changeButton) changeButton.disabled = Boolean(customer.owner_pending);
  }

`, "phone assurance render");
replaceRange("  function regularOrderCards() {", "  function renderOrders() {", `  function regularOrderCards() {
    const rows = state.data.orders || [];
    if (!rows.length) return empty("暂无普通订单");
    return rows
      .map((item) => {
        const time = item.paid_at || item.created_at || "";
        const timeLabel = item.paid_at ? "支付时间" : (item.created_at ? "创建时间" : "时间");
        const refund = item.refund_label ? '<span>退款</span><strong>' + escapeHtml(item.refund_label) + "</strong>" : "";
        const detailAction = item.detail_url
          ? '<div class="row-actions"><button class="btn primary" type="button" data-order-detail-url="' + escapeHtml(item.detail_url) + '">查看详情</button></div>'
          : "";
        return '<article class="card"><div class="card-title"><div><h3>' + escapeHtml(item.title || "未命名商品") + "</h3>" +
          '<div class="mini">' + escapeHtml(item.id || "") + '</div></div><div class="price">' + escapeHtml(item.amount_label || "") + "</div></div>" +
          '<div class="kv"><span>状态</span><strong>' + escapeHtml(item.status_label || "") + "</strong>" + refund +
          '<span>' + escapeHtml(timeLabel) + "</span><strong>" + escapeHtml(time) + "</strong></div>" + detailAction + "</article>";
      })
      .join("");
  }

`, "owner order render");
replaceRange("  function periodicOrderCards() {", "  function renderPeriodicOrders() {", `  function periodicOrderCards() {
    const rows = state.data.periodic_orders || [];
    if (!rows.length) return empty("暂无周期订单");
    return rows
        .map((item) => {
          const lastOrder = [item.last_out_trade_no || "", item.last_order_paid_at || ""].filter(Boolean).join(" · ");
          const detailAction = item.detail_url
            ? '<div class="row-actions"><button class="btn primary" type="button" data-order-detail-url="' + escapeHtml(item.detail_url || "") + '">查看详情</button></div>'
            : "";
          return (
            '<article class="card periodic-order-card"><div class="card-title"><div><h3>' + escapeHtml(item.title || "未命名周期商品") + "</h3>" +
            '<div class="mini">' + escapeHtml(lastOrder || item.product_code || "") + '</div></div><div class="price">' + escapeHtml(item.amount_label || "") + "</div></div>" +
            '<div class="kv"><span>生效时间</span><strong>' + escapeHtml(item.start_at || "未提供") + "</strong>" +
            '<span>到期时间</span><strong>' + escapeHtml(item.end_at || "未提供") + "</strong>" +
            '<span>剩余有效期</span><strong>' + escapeHtml(item.remaining_label || "未提供") + "</strong>" +
            '<span>周期</span><strong>' + escapeHtml(item.duration_label || "未提供") + "</strong>" +
            '<span>正式登录</span><strong>' + escapeHtml(huangyoucanBoolean(item, "huangyoucan_formally_logged_in", "是", "否")) + "</strong>" +
            '<span>token 消耗</span><strong>' + escapeHtml(huangyoucanBoolean(item, "huangyoucan_has_token_usage", "有", "无")) + "</strong>" +
            '<span>学习计划进度</span><strong>' + escapeHtml(huangyoucanProgress(item)) + "</strong>" +
            '<span>近 7 天打开次数</span><strong>' + escapeHtml(huangyoucanMatched(item) ? String(Number(item.huangyoucan_open_count_7d || 0)) : "—") + "</strong>" +
            '<span>最后打开时间</span><strong>' + escapeHtml(huangyoucanLastOpen(item)) + "</strong></div>" +
            '<div class="field periodic-remark"><div class="field-title">备注</div>' +
            '<textarea class="textarea periodic-remark-textarea" data-periodic-order-remark="' + escapeHtml(item.id || "") + '">' + escapeHtml(item.remark || "") + "</textarea></div>" +
            detailAction + "</article>"
          );
        })
        .join("");
  }

`, "owner periodic render");
replaceRange("  async function saveMobile() {", "  async function boot(options) {", `  async function saveMobile() {
    confirmMobileButton.disabled = true;
    mobileStatus.textContent = "正在保存…";
    try {
      const customer = (state.workbench || {}).customer || {};
      if (customer.owner_pending) {
        throw new Error("请先从企微侧边栏重新打开以确认当前员工身份");
      }
      const payload = await requestJson(endpoint("bindMobileUrl"), {
        method: "POST",
        body: JSON.stringify({
          external_userid: state.external_userid,
          owner_userid: state.owner_userid,
          bind_by_userid: state.bind_by_userid || state.owner_userid,
          mobile: mobileInput.value,
          force_rebind: Boolean(customer.mobile_bound !== undefined ? customer.mobile_bound : customer.is_bound && customer.mobile),
        }),
      });
      const binding = payload.binding || payload;
      const assurance = String(binding.phone_assurance || "declared").toLowerCase();
      state.workbench.customer.mobile = binding.mobile || mobileInput.value;
      state.workbench.customer.is_bound = true;
      state.workbench.customer.mobile_bound = assurance === "verified";
      state.workbench.customer.phone_assurance = assurance;
      renderTop();
      closeMobileModal();
      showToast(assurance === "verified" ? "手机号已验证" : "手机号已声明");
    } catch (error) {
      mobileStatus.textContent = error.message || "保存失败";
      mobileStatus.className = "status error";
      showToast(error.message || "保存失败", "error");
    } finally {
      confirmMobileButton.disabled = false;
    }
  }

`, "declared phone render");
replaceRange("    const materialSendButton = event.target.closest(\"[data-material-send]\");", "    const productTypeButton = event.target.closest(\"[data-product-type]\");", `    const materialSendButton = event.target.closest("[data-material-send]");
    if (materialSendButton) {
      if (materialSendButton.dataset.sending === "true") return;
      const label = materialSendButton.textContent;
      materialSendButton.disabled = true;
      materialSendButton.dataset.sending = "true";
      materialSendButton.textContent = "发送中…";
      try {
        await sendMaterial(materialSendButton.dataset.materialSend);
      } finally {
        delete materialSendButton.dataset.sending;
        materialSendButton.textContent = label;
        materialSendButton.disabled = false;
      }
      return;
    }
`, "material in-flight state");
replaceRange("    const productSendButton = event.target.closest(\"[data-product-send]\");", "    const orderDetailButton = event.target.closest(\"[data-order-detail-url]\");", `    const productSendButton = event.target.closest("[data-product-send]");
    if (productSendButton) {
      if (productSendButton.dataset.sending === "true") return;
      const label = productSendButton.textContent;
      productSendButton.disabled = true;
      productSendButton.dataset.sending = "true";
      productSendButton.textContent = "发送中…";
      try {
        await sendProduct(productSendButton.dataset.productSend, productSendButton.dataset.productKind || state.productType);
      } finally {
        delete productSendButton.dataset.sending;
        productSendButton.textContent = label;
        productSendButton.disabled = false;
      }
      return;
    }
`, "product in-flight state");


// Questionnaire and timeline cursor contracts are owned by Customer. Preserve
// their opaque continuation tokens rather than translating them to donor
// offsets; the standard list gets an explicit more action for multi-page data.
replaceRange("  function renderQuestionnaires() {", "  function renderProducts() {", `  function renderQuestionnaires() {
    const rows = state.data.questionnaires || [];
    const pager = state.questionnairePager || { has_more: false };
    if (!rows.length) {
      content.innerHTML = panel("问卷", empty("暂无问卷记录"));
      return;
    }
    content.innerHTML = panel(
      "问卷",
      rows
        .map((item, index) => {
          const answers = item.answers || [];
          const count = String(item.answer_count || answers.length || 0) + "/" + String(item.total_count || item.answer_count || answers.length || 0) + " 题";
          return (
            '<article class="card" tabindex="-1" data-questionnaire-card="' + index + '" data-questionnaire-submission-id="' + escapeHtml(item.submission_id || item.id || "") + '" data-questionnaire-id="' + escapeHtml(item.questionnaire_id || "") + '">' +
            '<div class="card-title"><div><h3>' + escapeHtml(item.title || "未命名问卷") + "</h3>" +
            '<div class="mini">' + escapeHtml([item.submitted_at || "", count].filter(Boolean).join(" · ")) + "</div></div></div>" +
            '<div class="row-actions"><button class="btn primary" type="button" data-toggle-questionnaire="' + index + '">查看答案</button></div>' +
            '<div class="questions">' +
            answers.map((answer) => '<div class="question"><b>' + escapeHtml(answer.question || "未命名问题") + "</b><em>" + escapeHtml(answer.answer || "未填写") + "</em></div>").join("") +
            "</div></article>"
          );
        })
        .join("") +
        (pager.has_more ? '<div class="row-actions"><button class="btn ghost" type="button" data-load-more-questionnaires>加载更多</button></div>' : "")
    );
  }

`, "questionnaire cursor render");

const questionnaireBranch = `    if (tab === "questionnaires") {
      const payload = await requestPanelJson("questionnaires", queryUrl(endpoint("questionnairesUrl"), customerContextQuery()));
      state.data.questionnaires = payload.questionnaires || [];
    } else if (tab === "products") {`;
const questionnaireBranchReplacement = `    if (tab === "questionnaires") {
      await loadQuestionnaires({ reset: true });
    } else if (tab === "products") {`;
once(questionnaireBranch, "questionnaire load branch");
js = js.replace(questionnaireBranch, questionnaireBranchReplacement);
replaceRange("  async function loadOrders(type) {", "  async function loadMaterials(type) {", `  async function loadQuestionnaires(options) {
    const reset = Boolean(options && options.reset);
    const pager = state.questionnairePager || { has_more: false, next_cursor: "" };
    const cursor = reset ? "" : String(pager.next_cursor || "");
    const params = { limit: 20 };
    if (cursor) params.cursor = cursor;
    const payload = await requestPanelJson("questionnaires", queryUrl(endpoint("questionnairesUrl"), params));
    const prior = reset ? [] : (state.data.questionnaires || []);
    state.data.questionnaires = prior.concat(payload.questionnaires || []);
    state.questionnairePager = { total: Number(payload.total || state.data.questionnaires.length), has_more: Boolean(payload.has_more), next_cursor: String(payload.next_cursor || "") };
  }

  async function loadOrders(type) {
    const normalized = type === "periodic" ? "periodic" : "regular";
    const cacheKey = "orders:" + normalized;
    if (state.loaded[cacheKey]) return;
    const panelKey = normalized === "periodic" ? "periodic_orders" : "orders";
    const url = normalized === "periodic" ? endpoint("periodicOrdersUrl") : endpoint("ordersUrl");
    const payload = await requestPanelJson(panelKey, queryUrl(url, customerContextQuery()));
    if (payload.customer) {
      state.workbench.customer = Object.assign({}, state.workbench.customer || {}, payload.customer);
      renderTop();
    }
    if (normalized === "periodic") {
      writeDebug("periodic orders response", payload.diagnostics || {});
      state.data.periodic_orders = payload.periodic_orders || [];
    } else {
      writeDebug("orders response", payload.diagnostics || {});
      state.data.orders = payload.orders || [];
    }
    state.loaded[cacheKey] = true;
  }

`, "questionnaire cursor loader");
replaceRange("  async function loadTimeline(options) {", "  async function switchProfileView(view) {", `  async function loadTimeline(options) {
    const reset = Boolean(options && options.reset);
    const force = Boolean(options && options.force);
    const currentTimeline = state.data.timeline || { items: [], next_cursor: "" };
    const cursor = reset ? "" : String(currentTimeline.next_cursor || "");
    const requestVersion = reset ? ++state.timelineRequestVersion : state.timelineRequestVersion;
    const params = { limit: 20 };
    if (cursor) params.cursor = cursor;
    const url = queryUrl(endpoint("timelineUrl"), params);
    if (force) clearPanelCache("timeline");
    const payload = await requestPanelJson("timeline", url);
    if (requestVersion !== state.timelineRequestVersion) return;
    const current = reset ? [] : (state.data.timeline.items || []);
    state.data.timeline = {
      items: current.concat(payload.items || []),
      total: Number(payload.total || current.length + (payload.items || []).length),
      has_more: Boolean(payload.has_more),
      next_cursor: String(payload.next_cursor || ""),
    };
  }

`, "timeline cursor loader");
replaceRange("    const materialTypeButton = event.target.closest(\"[data-material-type]\");", "    const materialKeywordButton = event.target.closest(\"[data-material-keyword]\");", `    const moreQuestionnairesButton = event.target.closest("[data-load-more-questionnaires]");
    if (moreQuestionnairesButton) {
      moreQuestionnairesButton.disabled = true;
      try {
        await loadQuestionnaires({ reset: false });
        if (state.activeTab === "questionnaires") renderQuestionnaires();
      } catch (error) {
        showToast(error.message || "加载更多失败", "error");
        moreQuestionnairesButton.disabled = false;
      }
      return;
    }
    const materialTypeButton = event.target.closest("[data-material-type]");
    if (materialTypeButton) {
      await switchMaterialType(materialTypeButton.dataset.materialType);
      return;
    }
`, "questionnaire more action");

// The release template intentionally omits this raw identifier field.
for (const fragment of [
  '    document.getElementById("customer-external-userid").textContent = state.external_userid ? "外部联系人 ID " + state.external_userid : "";\n',
  '    document.getElementById("customer-external-userid").textContent = externalUserid ? "外部联系人 ID " + externalUserid : "";\n',
]) {
  // renderTop and boot are replaced above; they already omit the identifiers.
  // Keep this cleanup idempotent so a future independent source-range adapter
  // cannot accidentally restore either raw external identifier.
  if (js.includes(fragment)) js = js.replace(fragment, "");
}

for (const forbidden of ["other_staff_messages", "其他客服聊天", "chat_activity", "other-staff-messages", "/api/sidebar/v2/other-staff-messages"]) {
  if (js.includes(forbidden)) throw new Error(`removed chat capability survived overlay: ${forbidden}`);
}
fs.mkdirSync(path.dirname(target), { recursive: true });
fs.writeFileSync(target, js);
const checked = spawnSync(process.execPath, ["--check", target], { encoding: "utf8" });
if (checked.status !== 0) throw new Error(`generated overlay syntax invalid: ${checked.stderr || checked.stdout}`);
console.log(JSON.stringify({ source_sha256: want, target, chat_dispatch_removed: true, trusted_bridge: true }));
