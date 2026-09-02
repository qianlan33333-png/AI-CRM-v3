(function () {
  "use strict";

  // This is a presentation-only shell.  Domain-owned sidebar endpoints are
  // exposed as data-* contracts by the template, but this file deliberately
  // performs no browser request or provider call while those capabilities are
  // pending.
  const root = document.getElementById("sidebar-workbench-root");
  if (!root) return;

  const content = document.getElementById("content");
  const tabs = Array.from(root.querySelectorAll("[data-tab]"));
  const mobileModal = document.getElementById("mobile-modal");
  const mobileInput = document.getElementById("mobile-input");
  const mobileStatus = document.getElementById("mobile-status");
  const toast = document.getElementById("toast");
  const confirmButton = document.getElementById("confirm-mobile-button");
  const labels = {
    profile: "核心画像",
    questionnaires: "问卷",
    products: "商品",
    orders: "订单",
    coupons: "优惠券",
    materials: "素材",
  };

  function setContent(tab) {
    if (!content) return;
    const panel = document.createElement("section");
    panel.className = "panel";
    panel.dataset.panel = tab;

    const head = document.createElement("div");
    head.className = "head";
    const title = document.createElement("h2");
    title.textContent = labels[tab] || "侧边栏";
    head.appendChild(title);

    const state = document.createElement("div");
    state.className = "status";
    const strong = document.createElement("strong");
    const message = document.createElement("span");
    if (tab === "profile") {
      strong.textContent = "正在识别当前客户…";
      message.textContent = "等待可信的企微上下文后显示客户信息。";
    } else {
      strong.textContent = "功能待接入";
      message.textContent = labels[tab] + "模块将在对应领域 API、权限与 Journey 完成后接入。";
    }
    state.appendChild(strong);
    state.appendChild(message);
    panel.appendChild(head);
    panel.appendChild(state);
    content.replaceChildren(panel);
  }

  function selectTab(tab) {
    tabs.forEach(function (button) {
      button.classList.toggle("active", button.dataset.tab === tab);
      button.setAttribute("aria-selected", button.dataset.tab === tab ? "true" : "false");
    });
    setContent(tab);
  }

  function showModal() {
    if (!mobileModal) return;
    mobileModal.classList.remove("hidden");
    if (mobileStatus) mobileStatus.textContent = "";
    if (mobileInput) {
      mobileInput.value = "";
      mobileInput.focus();
    }
  }

  function hideModal() {
    if (mobileModal) mobileModal.classList.add("hidden");
  }

  function showToast(message) {
    if (!toast) return;
    toast.textContent = message;
    toast.classList.remove("hidden");
    window.setTimeout(function () {
      toast.classList.add("hidden");
    }, 2600);
  }

  tabs.forEach(function (button) {
    button.addEventListener("click", function () {
      selectTab(button.dataset.tab || "profile");
    });
  });

  const changeButton = document.getElementById("change-mobile-button");
  if (changeButton) changeButton.addEventListener("click", showModal);
  const closeButton = document.getElementById("close-mobile-modal");
  if (closeButton) closeButton.addEventListener("click", hideModal);
  const cancelButton = document.getElementById("cancel-mobile-button");
  if (cancelButton) cancelButton.addEventListener("click", hideModal);

  if (confirmButton) {
    confirmButton.addEventListener("click", function () {
      const value = mobileInput ? mobileInput.value.trim() : "";
      if (!value) {
        if (mobileStatus) mobileStatus.textContent = "请输入手机号";
        return;
      }
      // Keep the write boundary explicit: no request is sent by the shell.
      if (mobileStatus) mobileStatus.textContent = "手机号绑定功能待接入";
      showToast("手机号绑定功能待接入");
    });
  }

  if (mobileModal) {
    mobileModal.addEventListener("click", function (event) {
      if (event.target === mobileModal) hideModal();
    });
  }

  selectTab("profile");
}());
