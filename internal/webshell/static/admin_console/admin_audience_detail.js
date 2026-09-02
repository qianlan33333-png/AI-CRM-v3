// Extracted from AI-CRM's ai_audience_package_detail.html. This shell-only
// adapter keeps the original secondary navigation and deliberately omits all
// business API loading and mutation code until the v3 capability is mounted.
(() => {
  const panelButtons = document.querySelectorAll("[data-panel]");
  const action = document.getElementById("saveCurrentDimensionBtn");
  const manualRefresh = document.getElementById("manualRefreshBtn");
  let currentPanel = "basic";

  const updateCurrentAction = () => {
    action.textContent = ["members", "records"].includes(currentPanel) ? "刷新列表" : "保存当前维度";
    manualRefresh.hidden = currentPanel === "records";
  };

  const showPanel = (key) => {
    currentPanel = key;
    panelButtons.forEach((button) => {
      button.classList.toggle("active", button.dataset.panel === key);
    });
    document.querySelectorAll(".ai-panel").forEach((panel) => {
      panel.classList.toggle("active", panel.id === `panel-${key}`);
    });
    updateCurrentAction();
  };

  panelButtons.forEach((button) => {
    button.addEventListener("click", () => showPanel(button.dataset.panel));
  });
  document.querySelectorAll(".ai-page button:not([data-panel]), .ai-page input, .ai-page textarea, .ai-page select").forEach((control) => {
    control.disabled = true;
    control.setAttribute("aria-disabled", "true");
  });
  ["sendRecordDrawerMask", "sendRecordDrawer"].forEach((id) => {
    const overlay = document.getElementById(id);
    if (overlay) {
      overlay.hidden = true;
      overlay.style.display = "none";
    }
  });
  showPanel("basic");
})();
