(function () {
  "use strict";

  const root = document.querySelector("[data-admin-access-root]");
  if (!root) return;

  const usersURL = root.dataset.usersUrl || "/api/admin/access/users";
  const userResourceURL = usersURL === "/api/admin/access/users"
    ? "/api/admin/access/users/"
    : usersURL.replace(/\/$/, "") + "/";
  const api = {
    users: usersURL,
    disable: function (id) {
      return userResourceURL + encodeURIComponent(String(id)) + "/disable";
    },
    password: function (id) {
      return userResourceURL + encodeURIComponent(String(id)) + "/password";
    },
    wecom: function (id) {
      return userResourceURL + encodeURIComponent(String(id)) + "/wecom-userid";
    },
    roles: function (id) {
      return userResourceURL + encodeURIComponent(String(id)) + "/roles";
    },
  };

  const elements = {
    alert: document.getElementById("admin-access-alert"),
    createForm: document.getElementById("admin-access-create-form"),
    refresh: document.getElementById("admin-access-refresh"),
    listStatus: document.getElementById("admin-access-list-status"),
    loading: document.getElementById("admin-access-loading"),
    empty: document.getElementById("admin-access-empty"),
    listError: document.getElementById("admin-access-list-error"),
    listErrorMessage: document.getElementById("admin-access-list-error-message"),
    tableWrap: document.getElementById("admin-access-table-wrap"),
    usersBody: document.getElementById("admin-access-users-body"),
    editor: document.getElementById("admin-access-editor"),
    editorUser: document.getElementById("admin-access-editor-user"),
    editorClose: document.getElementById("admin-access-editor-close"),
    selectedID: document.getElementById("admin-access-selected-id"),
    bindingForm: document.getElementById("admin-access-binding-form"),
    wecomInput: document.getElementById("admin-access-wecom-userid"),
    unbind: document.getElementById("admin-access-unbind"),
    rolesForm: document.getElementById("admin-access-roles-form"),
    passwordForm: document.getElementById("admin-access-password-form"),
    newPassword: document.getElementById("admin-access-new-password"),
  };

  const roleLabels = {
    super_admin: "超级管理员",
    admin: "管理员",
    viewer: "只读",
  };
  const errorLabels = {
    authentication_required: "登录状态已失效，请重新登录。",
    permission_denied: "当前账号没有执行该操作的权限。",
    csrf_required: "页面安全令牌已失效，请刷新后重试。",
    invalid_request: "请求内容不完整或格式不正确。",
    invalid_credentials: "账号或密码不正确，请重试。",
    rate_limited: "操作过于频繁，请稍后重试。",
    not_found: "员工记录不存在，请刷新列表。",
    conflict: "员工信息发生冲突，请刷新后重试。",
    internal_error: "服务暂时不可用，请稍后重试。",
  };

  let users = [];
  let selectedID = "";

  function readCSRFCookie() {
    const parts = String(document.cookie || "").split(";");
    for (let index = 0; index < parts.length; index += 1) {
      const part = parts[index].trim();
      if (part.indexOf("aicrm_admin_csrf=") !== 0) continue;
      const raw = part.slice("aicrm_admin_csrf=".length);
      try {
        return decodeURIComponent(raw);
      } catch (_error) {
        return raw;
      }
    }
    return "";
  }

  function setAlert(message, tone) {
    if (!elements.alert) return;
    elements.alert.textContent = message || "";
    elements.alert.className = "admin-alert" + (tone === "success" ? " admin-alert--success" : " admin-alert--error");
    elements.alert.hidden = !message;
  }

  function errorMessage(error, fallback) {
    const payload = (error && error.payload) || {};
    const code = String(payload.error || payload.error_code || "").trim();
    if (errorLabels[code]) return errorLabels[code];
    if (error && error.status === 401) return errorLabels.authentication_required;
    if (error && error.status === 403) return "当前账号没有执行该操作的权限。";
    return fallback || "请求失败，请稍后重试。";
  }

  async function requestJSON(url, options) {
    const requestOptions = options || {};
    const headers = new Headers(requestOptions.headers || {});
    headers.set("Accept", "application/json");
    if (requestOptions.body !== undefined) headers.set("Content-Type", "application/json");
    // The CSRF token is read only for the request header. It is never rendered,
    // logged, or retained in application state.
    headers.set("X-CSRF-Token", readCSRFCookie());
    const response = await fetch(url, {
      ...requestOptions,
      headers: headers,
      credentials: "same-origin",
      cache: "no-store",
    });
    const raw = await response.text();
    let payload = {};
    if (raw) {
      try {
        payload = JSON.parse(raw);
      } catch (_error) {
        payload = {};
      }
    }
    if (!response.ok || payload.ok === false) {
      const failure = new Error(errorMessage({ status: response.status, payload: payload }, "请求失败，请稍后重试。"));
      failure.status = response.status;
      failure.payload = payload;
      throw failure;
    }
    return payload;
  }

  function textCell(value, className) {
    const cell = document.createElement("td");
    if (className) cell.className = className;
    cell.textContent = value;
    return cell;
  }

  function roleText(values) {
    if (!Array.isArray(values) || values.length === 0) return "未设置";
    return values.map(function (value) {
      const code = String(value || "").trim();
      return roleLabels[code] ? roleLabels[code] + "（" + code + "）" : code;
    }).filter(Boolean).join("、") || "未设置";
  }

  function lastLoginText(value) {
    const raw = String(value || "").trim();
    if (!raw) return "从未登录";
    const date = new Date(raw);
    if (Number.isNaN(date.getTime())) return raw;
    try {
      return date.toLocaleString("zh-CN", { dateStyle: "medium", timeStyle: "short" });
    } catch (_error) {
      return raw;
    }
  }

  function actionButton(label, action, user) {
    const button = document.createElement("button");
    button.className = "admin-button admin-button--ghost";
    button.type = "button";
    button.textContent = label;
    button.dataset.accessAction = action;
    button.dataset.userId = String(user.id || "");
    button.dataset.userName = String(user.display_name || user.username || "");
    return button;
  }

  function renderUsers() {
    if (!elements.usersBody) return;
    elements.usersBody.replaceChildren();
    if (!users.length) {
      elements.tableWrap.hidden = true;
      elements.empty.hidden = false;
      return;
    }
    elements.empty.hidden = true;
    elements.tableWrap.hidden = false;
    users.forEach(function (user) {
      const row = document.createElement("tr");
      row.dataset.userId = String(user.id || "");
      row.appendChild(textCell(String(user.username || ""), "admin-access-table__name"));
      row.appendChild(textCell(String(user.display_name || "")));
      row.appendChild(textCell(String(user.wecom_userid || "未绑定"), "admin-access-table__muted"));

      const statusCell = document.createElement("td");
      const status = document.createElement("span");
      status.className = "admin-access-status" + (user.active ? "" : " is-inactive");
      status.textContent = user.active ? "可登录" : "已停用";
      statusCell.appendChild(status);
      row.appendChild(statusCell);
      row.appendChild(textCell(roleText(user.roles)));
      row.appendChild(textCell(lastLoginText(user.last_login_at), "admin-access-table__muted"));

      const actionsCell = document.createElement("td");
      const actions = document.createElement("div");
      actions.className = "admin-access-actions";
      actions.appendChild(actionButton("编辑", "edit", user));
      if (user.active) actions.appendChild(actionButton("停用", "disable", user));
      actionsCell.appendChild(actions);
      row.appendChild(actionsCell);
      elements.usersBody.appendChild(row);
    });
  }

  function setLoading(loading) {
    elements.loading.hidden = !loading;
    elements.refresh.disabled = loading;
    if (loading) elements.listError.hidden = true;
  }

  async function loadUsers() {
    setLoading(true);
    if (elements.listStatus) elements.listStatus.textContent = "正在加载员工列表…";
    try {
      const payload = await requestJSON(api.users, { method: "GET" });
      const result = Array.isArray(payload) ? payload : payload.users;
      users = Array.isArray(result) ? result : [];
      renderUsers();
      if (elements.listStatus) elements.listStatus.textContent = users.length + " 名员工";
      elements.listError.hidden = true;
      return true;
    } catch (error) {
      users = [];
      renderUsers();
      elements.tableWrap.hidden = true;
      elements.empty.hidden = true;
      elements.listError.hidden = false;
      elements.listErrorMessage.textContent = errorMessage(error, "员工列表暂时不可用，请稍后重试。");
      if (elements.listStatus) elements.listStatus.textContent = "员工列表不可用";
      return false;
    } finally {
      setLoading(false);
    }
  }

  function selectedUser() {
    return users.find(function (user) {
      return String(user.id || "") === selectedID;
    }) || null;
  }

  function setEditorRoleValues(values) {
    const roles = Array.isArray(values) ? values.map(function (value) { return String(value || ""); }) : [];
    if (!elements.rolesForm) return;
    Array.from(elements.rolesForm.querySelectorAll('input[name="roles"]')).forEach(function (input) {
      input.checked = roles.indexOf(input.value) >= 0;
    });
  }

  function openEditor(user) {
    const id = String(user && user.id || "").trim();
    if (!id) return;
    selectedID = id;
    elements.selectedID.value = id;
    elements.wecomInput.value = String(user.wecom_userid || "");
    elements.editorUser.textContent = String(user.display_name || user.username || "员工") + " · " + String(user.username || "");
    setEditorRoleValues(user.roles);
    elements.editor.hidden = false;
    elements.editor.scrollIntoView({ block: "nearest" });
    elements.wecomInput.focus();
  }

  function closeEditor() {
    selectedID = "";
    elements.selectedID.value = "";
    elements.editor.hidden = true;
  }

  async function mutate(url, body, successMessage, resetForm) {
    if (!selectedID && url !== api.users) {
      setAlert("请先从员工列表选择一名员工。", "error");
      return false;
    }
    try {
      await requestJSON(url, { method: "POST", body: JSON.stringify(body || {}) });
      const refreshed = await loadUsers();
      if (!refreshed) {
        setAlert("操作已提交，但员工列表刷新失败，请刷新确认结果。", "error");
        return false;
      }
      if (typeof resetForm === "function") resetForm();
      setAlert(successMessage, "success");
      const user = selectedUser();
      if (user) openEditor(user);
      return true;
    } catch (error) {
      setAlert(errorMessage(error), "error");
      return false;
    }
  }

  function formRoles(form) {
    return Array.from(form.querySelectorAll('input[name="roles"]:checked')).map(function (input) {
      return input.value;
    });
  }

  if (elements.createForm) {
    elements.createForm.addEventListener("submit", async function (event) {
      event.preventDefault();
      const form = event.currentTarget;
      const roles = formRoles(form);
      if (!roles.length) {
        setAlert("请至少选择一个角色。", "error");
        return;
      }
      const submit = form.querySelector('button[type="submit"]');
      submit.disabled = true;
      const data = new FormData(form);
      try {
        await requestJSON(api.users, {
          method: "POST",
          body: JSON.stringify({
            username: String(data.get("username") || "").trim(),
            display_name: String(data.get("display_name") || "").trim(),
            password: String(data.get("password") || ""),
            roles: roles,
          }),
        });
        form.reset();
        const refreshed = await loadUsers();
        if (!refreshed) {
          setAlert("员工已创建，但列表刷新失败，请刷新确认结果。", "error");
          return;
        }
        setAlert("员工已新增。", "success");
      } catch (error) {
        setAlert(errorMessage(error), "error");
      } finally {
        submit.disabled = false;
      }
    });
  }

  if (elements.usersBody) {
    elements.usersBody.addEventListener("click", function (event) {
      const button = event.target.closest("button[data-access-action]");
      if (!button) return;
      const id = button.dataset.userId || "";
      const user = users.find(function (item) { return String(item.id || "") === id; });
      if (!user) {
        setAlert("员工记录已变化，请刷新列表。", "error");
        return;
      }
      if (button.dataset.accessAction === "edit") {
        openEditor(user);
        return;
      }
      if (button.dataset.accessAction === "disable") {
        if (typeof window.confirm === "function" && !window.confirm("确定停用该员工账号吗？")) return;
        button.disabled = true;
        mutate(api.disable(id), {}).finally(function () {
          button.disabled = false;
        });
      }
    });
  }

  if (elements.refresh) elements.refresh.addEventListener("click", function () {
    setAlert("", "");
    loadUsers();
  });
  if (elements.editorClose) elements.editorClose.addEventListener("click", closeEditor);

  if (elements.bindingForm) elements.bindingForm.addEventListener("submit", function (event) {
    event.preventDefault();
    const value = elements.wecomInput.value.trim();
    if (!value) {
      setAlert("请输入企微 userid；解除绑定请使用“解除绑定”。", "error");
      return;
    }
    const submit = event.currentTarget.querySelector('button[type="submit"]');
    submit.disabled = true;
    mutate(api.wecom(selectedID), { wecom_userid: value }, "企微 userid 已更新。", null).finally(function () {
      submit.disabled = false;
    });
  });

  if (elements.unbind) elements.unbind.addEventListener("click", function () {
    if (!selectedID) {
      setAlert("请先从员工列表选择一名员工。", "error");
      return;
    }
    elements.unbind.disabled = true;
    mutate(api.wecom(selectedID), { wecom_userid: "" }, "企微绑定已解除。", function () {
      elements.wecomInput.value = "";
    }).finally(function () {
      elements.unbind.disabled = false;
    });
  });

  if (elements.rolesForm) elements.rolesForm.addEventListener("submit", function (event) {
    event.preventDefault();
    const roles = formRoles(event.currentTarget);
    if (!roles.length) {
      setAlert("请至少选择一个角色。", "error");
      return;
    }
    const submit = event.currentTarget.querySelector('button[type="submit"]');
    submit.disabled = true;
    mutate(api.roles(selectedID), { roles: roles }, "角色已更新。", null).finally(function () {
      submit.disabled = false;
    });
  });

  if (elements.passwordForm) elements.passwordForm.addEventListener("submit", function (event) {
    event.preventDefault();
    const form = event.currentTarget;
    const password = elements.newPassword.value;
    if (!password) {
      setAlert("请输入新密码。", "error");
      return;
    }
    const submit = form.querySelector('button[type="submit"]');
    submit.disabled = true;
    mutate(api.password(selectedID), { password: password }, "密码已重置。", function () {
      form.reset();
    }).finally(function () {
      submit.disabled = false;
    });
  });

  loadUsers();
}());
