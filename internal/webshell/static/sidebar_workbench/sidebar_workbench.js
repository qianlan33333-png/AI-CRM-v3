(function () {
  "use strict";

  const root = document.getElementById("sidebar-workbench-root");
  if (!root) return;

  // The bootstrap allowlist is intentional: unfinished sidebar tabs never
  // become network clients by accident.
  const SIDEBAR_BOOTSTRAP_APIS = Object.freeze({
    jssdkConfig: "/api/sidebar/jssdk-config",
    contextToken: "/api/sidebar/context-token",
    workbench: "/api/sidebar/v2/workbench",
    oauthStart: "/api/sidebar/oauth/start?next=/sidebar/bind-mobile",
  });

  const content = document.getElementById("content");
  const tabs = Array.from(root.querySelectorAll("[data-tab]"));
  const startupStatus = document.getElementById("sidebar-startup-status");
  const customerName = document.getElementById("customer-name");
  const customerIDNode = document.getElementById("customer-id");
  const bindingState = document.getElementById("binding-state");
  const customerMobile = document.getElementById("customer-mobile");
  const customerExternalUserid = document.getElementById("customer-external-userid");
  const workflowTitle = document.getElementById("workflow-title");
  const mobileModal = document.getElementById("mobile-modal");
  const mobileInput = document.getElementById("mobile-input");
  const mobileStatus = document.getElementById("mobile-status");
  const toast = document.getElementById("toast");
  const labels = {
    profile: "核心画像",
    questionnaires: "问卷",
    products: "商品",
    orders: "订单",
    coupons: "优惠券",
    materials: "素材",
  };
  const apiErrorLabels = {
    authentication_required: "当前员工会话已失效。",
    viewer_session_required: "当前员工会话未建立。",
    permission_denied: "当前员工没有访问权限。",
    provider_unavailable: "企微服务暂时不可用。",
    external_userid_missing: "没有取得可信的外部联系人上下文。",
  };

  let activeTab = "profile";
  let selectedExternalUserid = "";
  let workbench = null;
  let sidebarContextToken = "";
  let toastTimer = null;

  function endpoint(name) {
    const fallback = name === "jssdkConfigUrl"
      ? SIDEBAR_BOOTSTRAP_APIS.jssdkConfig
      : name === "contextTokenUrl"
        ? SIDEBAR_BOOTSTRAP_APIS.contextToken
        : name === "workbenchUrl"
          ? SIDEBAR_BOOTSTRAP_APIS.workbench
          : "";
    return String(root.dataset[name] || fallback).trim();
  }

  function setShellState(state, message) {
    root.dataset.sidebarShellState = state;
    if (startupStatus) startupStatus.textContent = message || "";
    if (bindingState) {
      bindingState.className = "phone-state" + (state === "ready" ? "" : state === "error" ? " unbound" : " loading");
      bindingState.textContent = state === "ready" ? "已识别" : state === "error" ? "不可用" : "加载中...";
    }
  }

  function showToast(message, isError) {
    if (!toast) return;
    window.clearTimeout(toastTimer);
    toast.textContent = message || "";
    toast.className = "toast" + (isError ? " error" : "");
    toast.classList.remove("hidden");
    toastTimer = window.setTimeout(function () {
      toast.classList.add("hidden");
    }, 3200);
  }

  function safeJSON(text) {
    if (!text) return {};
    try {
      return JSON.parse(text);
    } catch (_error) {
      return {};
    }
  }

  function friendlyError(error, fallback) {
    const payload = (error && error.payload) || {};
    const code = String(payload.error || payload.error_code || payload.status || "").trim();
    if (apiErrorLabels[code]) return apiErrorLabels[code];
    if (error && error.status === 401) return apiErrorLabels.authentication_required;
    if (error && error.status === 403) return apiErrorLabels.permission_denied;
    return fallback || "侧边栏暂时不可用，请稍后重试。";
  }

  async function requestJSON(url, options) {
    const requestOptions = options || {};
    const response = await fetch(url, {
      ...requestOptions,
      headers: {
        Accept: "application/json",
        ...(requestOptions.body !== undefined ? { "Content-Type": "application/json" } : {}),
        ...(requestOptions.headers || {}),
      },
      credentials: "same-origin",
      cache: "no-store",
    });
    const payload = safeJSON(await response.text());
    if (!response.ok || payload.ok === false) {
      const error = new Error(friendlyError({ status: response.status, payload: payload }));
      error.status = response.status;
      error.payload = payload;
      throw error;
    }
    return payload;
  }

  function currentSignedURL() {
    // WeCom signatures cover the exact page URL without the fragment. Keep all
    // query parameters; changing them would invalidate the signature.
    return window.location.href.split("#")[0];
  }

  function jssdkURL() {
    const url = new URL(endpoint("jssdkConfigUrl"), window.location.origin);
    url.searchParams.set("url", currentSignedURL());
    return url.toString();
  }

  function firstValue(object, keys) {
    if (!object || typeof object !== "object") return "";
    for (let index = 0; index < keys.length; index += 1) {
      const value = String(object[keys[index]] || "").trim();
      if (value) return value;
    }
    return "";
  }

  function sdkConfigParts(payload) {
    const config = payload.config || {};
    const agentConfig = payload.agent_config || payload.agentConfig || {};
    return {
      corpID: firstValue(payload, ["corp_id", "corpId", "corpid", "appId"]),
      agentID: firstValue(payload, ["agent_id", "agentId", "agentid"]),
      config: config,
      agentConfig: agentConfig,
    };
  }

  function configureWeCom(payload) {
    if (!window.wx || typeof window.wx.config !== "function") {
      throw new Error("WeCom SDK unavailable");
    }
    const parts = sdkConfigParts(payload);
    if (!parts.corpID || !parts.agentID || !parts.config.timestamp || !parts.config.nonceStr || !parts.config.signature) {
      throw new Error("WeCom SDK configuration unavailable");
    }
    return new Promise(function (resolve, reject) {
      let settled = false;
      const timeout = window.setTimeout(function () {
        if (settled) return;
        settled = true;
        reject(new Error("WeCom SDK configuration timed out"));
      }, 6000);
      function finish(error) {
        if (settled) return;
        settled = true;
        window.clearTimeout(timeout);
        if (error) reject(error); else resolve(parts);
      }
      window.wx.ready(function () {
        if (typeof window.wx.agentConfig !== "function") {
          finish(new Error("WeCom agent SDK unavailable"));
          return;
        }
        if (!parts.agentConfig.timestamp || !parts.agentConfig.nonceStr || !parts.agentConfig.signature) {
          finish(new Error("WeCom agent configuration unavailable"));
          return;
        }
        window.wx.agentConfig({
          corpid: parts.corpID,
          agentid: String(parts.agentID),
          timestamp: Number(parts.agentConfig.timestamp),
          nonceStr: parts.agentConfig.nonceStr,
          signature: parts.agentConfig.signature,
          jsApiList: ["getCurExternalContact"],
          success: function () { finish(); },
          fail: function () { finish(new Error("WeCom agent configuration failed")); },
        });
      });
      window.wx.error(function () { finish(new Error("WeCom SDK configuration failed")); });
      window.wx.config({
        beta: true,
        debug: false,
        appId: parts.corpID,
        timestamp: Number(parts.config.timestamp),
        nonceStr: parts.config.nonceStr,
        signature: parts.config.signature,
        jsApiList: ["getCurExternalContact"],
      });
    });
  }

  function externalUseridFrom(payload) {
    return firstValue(payload, [
      "external_userid",
      "externalUserid",
      "external_userId",
      "externalUserId",
      "external_user_id",
      "user_id",
      "userId",
    ]);
  }

  function getCurrentExternalContact() {
    if (!window.wx || typeof window.wx.invoke !== "function") {
      return Promise.reject(new Error("WeCom contact context unavailable"));
    }
    return new Promise(function (resolve, reject) {
      let settled = false;
      const timeout = window.setTimeout(function () {
        if (settled) return;
        settled = true;
        reject(new Error("WeCom contact context timed out"));
      }, 6000);
      window.wx.invoke("getCurExternalContact", {}, function (payload) {
        if (settled) return;
        settled = true;
        window.clearTimeout(timeout);
        const externalUserid = externalUseridFrom(payload || {});
        if (!externalUserid) {
          reject(new Error("No trusted external contact context"));
          return;
        }
        resolve(externalUserid);
      });
    });
  }

  function shouldStartOAuth(error) {
    const payload = (error && error.payload) || {};
    const code = String(payload.error || payload.error_code || payload.sidebar_owner_token_status || "").trim();
    return code === "viewer_session_required" || code === "authentication_required" || (error && error.status === 401);
  }

  function startOAuth() {
    // OAuth establishes an independent sidebar staff session. It never shares
    // the admin cookie and does not carry a customer identity in the URL.
    window.location.assign(SIDEBAR_BOOTSTRAP_APIS.oauthStart);
  }

  function renderProfile() {
    const customer = (workbench && workbench.customer) || {};
    const customerID = firstValue(workbench, ["customer_id", "customerId"]) || firstValue(customer, ["customer_id", "customerId", "id"]);
    const status = firstValue(workbench, ["status", "customer_status"]) || firstValue(customer, ["status", "customer_status"]);
    if (customerName) customerName.textContent = customerID ? "客户工作台" : "客户信息不可用";
    if (customerIDNode) customerIDNode.textContent = customerID ? "customer_id: " + customerID : "customer_id: 不可用";
    if (customerMobile) customerMobile.textContent = "";
    if (customerExternalUserid) customerExternalUserid.textContent = selectedExternalUserid ? "外部联系人上下文已确认" : "";
    if (workflowTitle) workflowTitle.textContent = status ? "状态：" + status : "状态不可用";
    setShellState("ready", customerID || status ? "客户工作台已加载" : "客户工作台已返回，但缺少客户状态字段");
  }

  function renderUnavailableProfile() {
    if (customerName) customerName.textContent = "客户信息不可用";
    if (customerIDNode) customerIDNode.textContent = "customer_id: 不可用";
    if (customerMobile) customerMobile.textContent = "";
    if (customerExternalUserid) customerExternalUserid.textContent = "";
    if (workflowTitle) workflowTitle.textContent = "status: 不可用";
  }

  function renderPanel(tab) {
    if (!content) return;
    const panel = document.createElement("section");
    panel.className = "panel";
    panel.dataset.panel = tab;
    const head = document.createElement("div");
    head.className = "head";
    const title = document.createElement("h2");
    title.textContent = labels[tab] || "侧边栏";
    head.appendChild(title);
    panel.appendChild(head);
    const state = document.createElement("div");
    state.className = "status";
    const strong = document.createElement("strong");
    const message = document.createElement("span");
    if (tab === "profile" && workbench) {
      strong.textContent = "客户工作台";
      message.textContent = "当前仅接入客户 ID 与状态。画像、问卷、商品、订单、优惠券和素材尚未接入。";
    } else if (tab === "profile") {
      strong.textContent = "客户信息不可用";
      message.textContent = "未取得可信的企微客户上下文，未展示客户或模拟数据。";
      state.classList.add("error");
    } else {
      strong.textContent = "功能待接入";
      message.textContent = labels[tab] + "模块尚未接入，不会请求业务接口。";
    }
    state.appendChild(strong);
    state.appendChild(message);
    panel.appendChild(state);
    content.replaceChildren(panel);
  }

  function selectTab(tab) {
    activeTab = labels[tab] ? tab : "profile";
    tabs.forEach(function (button) {
      const selected = button.dataset.tab === activeTab;
      button.classList.toggle("active", selected);
      button.setAttribute("aria-selected", selected ? "true" : "false");
    });
    renderPanel(activeTab);
  }

  function openMobileModal() {
    if (!mobileModal) return;
    mobileModal.classList.remove("hidden");
    if (mobileStatus) mobileStatus.textContent = "手机号绑定接口尚未接入。";
    if (mobileInput) {
      mobileInput.value = "";
      mobileInput.focus();
    }
  }

  function closeMobileModal() {
    if (mobileModal) mobileModal.classList.add("hidden");
  }

  async function bootstrap() {
    setShellState("identifying_customer", "正在加载企微身份…");
    try {
      const configPayload = await requestJSON(jssdkURL(), { method: "GET" });
      await configureWeCom(configPayload);
      selectedExternalUserid = await getCurrentExternalContact();
      setShellState("loading_context", "正在确认当前客户访问上下文…");
      const contextPayload = await requestJSON(endpoint("contextTokenUrl"), {
        method: "POST",
        body: JSON.stringify({ external_userid: selectedExternalUserid }),
      });
      sidebarContextToken = firstValue(contextPayload, ["context_token", "sidebar_owner_token", "token"]);
      if (!sidebarContextToken) {
        const contextError = new Error("Sidebar customer context unavailable");
        contextError.payload = contextPayload;
        // A successful response without a token is not by itself proof that
        // OAuth is missing; shouldStartOAuth uses the server's explicit state.
        contextError.status = 200;
        throw contextError;
      }
      const workbenchURL = new URL(endpoint("workbenchUrl"), window.location.origin);
      const workbenchPayload = await requestJSON(workbenchURL.toString(), {
        method: "GET",
        headers: { Authorization: "Bearer " + sidebarContextToken },
      });
      workbench = workbenchPayload;
      renderProfile();
      selectTab("profile");
    } catch (error) {
      if (shouldStartOAuth(error)) {
        startOAuth();
        return;
      }
      sidebarContextToken = "";
      workbench = null;
      setShellState("error", friendlyError(error, "侧边栏暂时不可用，请从企微侧边栏重新打开。"));
      renderUnavailableProfile();
      renderPanel("profile");
      showToast(friendlyError(error), true);
    }
  }

  tabs.forEach(function (button) {
    button.addEventListener("click", function () {
      selectTab(button.dataset.tab || "profile");
    });
  });
  const changeButton = document.getElementById("change-mobile-button");
  if (changeButton) changeButton.addEventListener("click", openMobileModal);
  const closeButton = document.getElementById("close-mobile-modal");
  if (closeButton) closeButton.addEventListener("click", closeMobileModal);
  const cancelButton = document.getElementById("cancel-mobile-button");
  if (cancelButton) cancelButton.addEventListener("click", closeMobileModal);
  const confirmButton = document.getElementById("confirm-mobile-button");
  if (confirmButton) confirmButton.addEventListener("click", function () {
    if (mobileStatus) mobileStatus.textContent = "手机号绑定接口尚未接入。";
    showToast("手机号绑定接口尚未接入。", true);
  });
  if (mobileModal) mobileModal.addEventListener("click", function (event) {
    if (event.target === mobileModal) closeMobileModal();
  });

  selectTab("profile");
  bootstrap();
}());
