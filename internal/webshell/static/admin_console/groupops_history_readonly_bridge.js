(function (global) {
  "use strict";

  var detailPath = /^\/api\/admin\/automation-conversion\/group-ops\/history\/plans\/[1-9][0-9]{0,18}\/nodes$/;
  var captured = null;
  var scheduled = false;

  function text(value) { return typeof value === "string" ? value : ""; }
  function attachments(value) {
    if (!Array.isArray(value)) return [];
    return value.map(function (item, index) {
      var row = item && typeof item === "object" && !Array.isArray(item) ? item : {};
      var kind = text(row.kind) || "附件";
      var id = row.id === undefined || row.id === null || row.id === "" ? "#" + (index + 1) : "#" + String(row.id);
      return { type_label: "历史素材 · " + kind, name: kind + " " + id, description: "原始附件引用（仅展示）" };
    });
  }

  function render() {
    scheduled = false;
    if (!captured || !Array.isArray(captured.items)) return;
    var host = document.querySelector("#group-history-secondary [data-history-rows]");
    var renderer = global.AICRMSendContentReadonlyDetail;
    if (!host || !renderer || typeof renderer.renderFull !== "function" || typeof renderer.escapeHtml !== "function") return;
    var rows = host.querySelectorAll("article");
    if (rows.length < captured.items.length) return;
    captured.items.forEach(function (item, index) {
      var article = rows[index];
      var raw = article && article.querySelector("details");
      if (!raw || raw.getAttribute("data-groupops-history-content") === "rendered") return;
      var content = document.createElement("section");
      content.setAttribute("data-groupops-history-content", "rendered");
      content.style.display = "grid";
      content.style.gap = "8px";
      content.innerHTML = '<div><span style="color:#667085">原操作标题：</span><span style="white-space:pre-wrap">' + renderer.escapeHtml(text(item.action_title)) + '</span></div>' + renderer.renderFull({
        content_text: text(item.text_content),
        content_basis_label: "历史正文（仅展示，不执行）",
        attachment_basis_label: "历史附件引用（仅展示，不读取素材）",
        attachments: attachments(item.attachments)
      });
      raw.replaceWith(content);
    });
  }

  function schedule() {
    if (scheduled) return;
    scheduled = true;
    global.setTimeout(render, 0);
  }

  function capture(response, input) {
    var url;
    try { url = new URL(typeof input === "string" ? input : input.url, global.location.href); } catch (_error) { return; }
    if (!detailPath.test(url.pathname) || !response || typeof response.clone !== "function") return;
    Promise.resolve(response.clone().json()).then(function (page) {
      if (page && page.source === "v1_history" && page.read_only === true && page.real_external_call_executed === false && Array.isArray(page.items)) {
        captured = page;
        schedule();
      }
    }).catch(function () {});
  }

  var previousFetch = global.fetch;
  if (typeof previousFetch === "function") {
    global.fetch = function (input, init) {
      return Promise.resolve(previousFetch.call(this, input, init)).then(function (response) {
        capture(response, input);
        return response;
      });
    };
  }
  new MutationObserver(schedule).observe(document.documentElement, { childList: true, subtree: true });
})(window);
