(function () {
  'use strict';

  function text(value) {
    return value == null ? '' : String(value);
  }

  function appendCell(row, value) {
    const cell = document.createElement('td');
    cell.textContent = text(value);
    cell.style.cssText = 'padding:8px;border-bottom:1px solid #f2f3f5;vertical-align:top';
    row.appendChild(cell);
  }
  function csrfToken() {
    const match = document.cookie.match(/(?:^|;\s*)(?:aicrm_admin_csrf|aicrm_csrf)=([^;]+)/);
    return match ? decodeURIComponent(match[1]) : '';
  }
  async function adminRequest(path, options) {
    if (window.AdminApi && typeof window.AdminApi.requestJson === 'function') return window.AdminApi.requestJson(path, options);
    const headers = new Headers((options && options.headers) || {});
    headers.set('X-CSRF-Token', csrfToken());
    headers.set('Idempotency-Key', 'survey-meta-' + crypto.randomUUID());
    const response = await fetch(path, Object.assign({credentials:'same-origin'}, options, {headers:headers}));
    if (!response.ok) {
      const error = new Error('HTTP ' + response.status);
      error.status = response.status;
      throw error;
    }
    return response.json();
  }

  function installQrFallback() {
    const page = document.body.dataset.page || '';
    if (!['questionnaires', 'questionnaireDetail', 'questionnaireOps'].includes(page)) return;
    const pending = new WeakSet();

    function watchQrBox(box) {
      if (!box || pending.has(box)) return;
      pending.add(box);
      setTimeout(function () {
        if (!box.isConnected || box.childElementCount > 0 || text(box.textContent).trim() !== '') return;
        const alert = document.createElement('div');
        alert.setAttribute('role', 'alert');
        alert.dataset.surveyQrFallback = 'true';
        alert.style.cssText = 'padding:16px;color:#d93026;text-align:center;line-height:1.6';
        alert.textContent = '二维码加载失败，请使用上方“复制”按钮复制链接。';
        box.replaceChildren(alert);
      }, 1200);
    }

    function scan() {
      if (!document || !document.body) return;
      watchQrBox(document.getElementById('shareQrBox'));
    }

    scan();
    new MutationObserver(scan).observe(document.body, { childList: true, subtree: true });
  }

  async function mount() {
    if (document.body.dataset.page !== 'questionnaireOps') return;
    const questionnaireID = new URLSearchParams(location.search).get('id') || '';
    if (!/^[1-9][0-9]*$/.test(questionnaireID)) return;
    const stage = document.getElementById('stage');
    if (!stage || document.querySelector('[data-survey-canonical-receipts]')) return;

    const panel = document.createElement('section');
    panel.dataset.surveyCanonicalReceipts = 'true';
    panel.style.cssText = 'margin:16px 20px;padding:16px;background:#fff;border:1px solid #dee0e3;border-radius:8px';
    const heading = document.createElement('h3');
    heading.textContent = '真实外部操作回执（只读）';
    heading.style.cssText = 'margin:0 0 6px;font-size:15px';
    const note = document.createElement('p');
    note.textContent = '显示 v3 标准状态及历史结果。历史回执不可重放；Provider 状态以当前配置为准。';
    note.style.cssText = 'margin:0 0 12px;color:#8f959e;font-size:12px';
    const content = document.createElement('div');
    content.setAttribute('role', 'status');
    content.textContent = '正在读取真实回执…';
    panel.append(heading, note, content);
    stage.insertAdjacentElement('afterend', panel);

    try {
      const response = await fetch('/api/admin/questionnaires/' + questionnaireID + '/operations', {
        credentials: 'same-origin',
        headers: { Accept: 'application/json' },
      });
      if (!response.ok) throw new Error('HTTP ' + response.status);
      const payload = await response.json();
      if (!payload || typeof payload.provider_enabled !== 'boolean' || typeof payload.real_external_call_executed !== 'boolean' || !Array.isArray(payload.items)) {
        throw new Error('invalid receipt contract');
      }
      const push = payload.external_push || {};
      const metadata = push.metadata && typeof push.metadata === 'object' ? push.metadata : {};
      const form = document.createElement('form');
      form.dataset.surveyPushMetadata = 'true';
      form.style.cssText = 'display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:8px;margin:0 0 14px;padding:12px;border:1px solid #eef0f2;border-radius:6px';
      [['推送类型', 'type'], ['有效期秒时间戳', 'expires_at_ts'], ['第几天', 'day'], ['频次', 'frequency'], ['备注', 'remark']].forEach(function (item) {
        const label = document.createElement('label'); label.textContent = item[0];
        const input = document.createElement('input'); input.name = item[1]; input.value = metadata[item[1]] == null ? '' : String(metadata[item[1]]); input.style.cssText = 'display:block;width:100%;box-sizing:border-box;margin-top:4px;height:28px;border:1px solid #dee0e3;border-radius:4px';
        label.appendChild(input); form.appendChild(label);
      });
      const params = document.createElement('div'); params.dataset.surveyPushParams = 'true'; params.style.cssText = 'grid-column:1/-1;display:grid;gap:6px'; form.appendChild(params);
      function paramRow(name, value) { const row = document.createElement('div'); row.style.cssText = 'display:grid;grid-template-columns:1fr 1fr auto;gap:6px'; row.innerHTML = '<input data-param-name placeholder="参数名"><input data-param-value placeholder="参数值"><button type="button">删除</button>'; row.querySelector('[data-param-name]').value = name || ''; row.querySelector('[data-param-value]').value = value || ''; row.querySelector('button').onclick = function () { row.remove(); }; params.appendChild(row); }
      Object.entries(metadata.custom_params || {}).forEach(function (item) { paramRow(item[0], item[1]); }); if (!params.children.length) paramRow('', '');
      const addParam = document.createElement('button'); addParam.type = 'button'; addParam.textContent = '添加自定义参数'; addParam.onclick = function () { paramRow('', ''); }; form.appendChild(addParam);
      const save = document.createElement('button'); save.type = 'submit'; save.textContent = '保存外推参数'; save.style.cssText = 'width:max-content;height:28px;border:0;border-radius:4px;background:#3370ff;color:#fff;padding:0 10px'; form.appendChild(save);
      form.addEventListener('submit', async function (event) {
        event.preventDefault();
        try {
          const next = {custom_params:{}}; ['type', 'expires_at_ts', 'day', 'frequency', 'remark'].forEach(function (key) { const value = form.elements[key].value.trim(); if (value) { const numeric = /^(day|frequency|expires_at_ts)$/.test(key); const parsed = numeric ? Number(value) : value; if (numeric && (!Number.isFinite(parsed) || parsed < 0)) throw new Error('invalid metadata field'); next[key] = parsed; } });
          params.querySelectorAll('div').forEach(function (row) { const name = row.querySelector('[data-param-name]').value.trim(); const value = row.querySelector('[data-param-value]').value; if (name) next.custom_params[name] = value; });
          // Fetch a single configuration revision, then require that exact
          // revision during PUT. A concurrent save must not be overwritten.
          const current = await adminRequest('/api/admin/questionnaires/' + questionnaireID + '/operations', {method:'GET', headers:{Accept:'application/json'}});
          if (!Number.isInteger(current.configuration_version) || current.configuration_version < 0) throw new Error('invalid configuration revision');
          const saved = await adminRequest('/api/admin/questionnaires/' + questionnaireID + '/operations/external-push', { method: 'PUT', headers: {'Content-Type':'application/json', Accept:'application/json'}, body: JSON.stringify({enabled: Boolean(current.external_push && current.external_push.enabled), configuration_reference: current.external_push && current.external_push.configuration_reference || '', metadata: next, configuration_version: current.configuration_version}) });
          push.enabled = saved.external_push.enabled; push.configuration_reference = saved.external_push.configuration_reference; push.metadata = saved.external_push.metadata; save.textContent = '已保存';
        } catch (_error) { save.textContent = _error && (_error.status === 409 || /\b409\b/.test(_error.message || '')) ? '配置已更新，请重新打开后再保存' : '保存失败'; }
      });
      content.replaceChildren();
      content.appendChild(form);
      if (payload.items.length === 0) {
        const empty = document.createElement('p'); empty.textContent = '暂无外部操作回执。'; content.appendChild(empty);
        return;
      }
      const table = document.createElement('table');
      table.style.cssText = 'width:100%;border-collapse:collapse;font-size:12px';
      const header = document.createElement('tr');
      ['发生时间', '操作', '真实状态', '次数', '失败分类', '执行边界'].forEach(function (label) {
        const cell = document.createElement('th');
        cell.textContent = label;
        cell.style.cssText = 'padding:8px;text-align:left;border-bottom:1px solid #dee0e3';
        header.appendChild(cell);
      });
      const head = document.createElement('thead');
      head.appendChild(header);
      const body = document.createElement('tbody');
      payload.items.forEach(function (item) {
        const row = document.createElement('tr');
        appendCell(row, item.occurred_at);
        appendCell(row, item.operation_kind);
        appendCell(row, item.status);
        appendCell(row, item.occurrence_count);
        appendCell(row, item.failure_category || '—');
        appendCell(row, item.read_only_legacy ? '历史只读 / 不可重放' : (payload.provider_enabled ? '本地意图 / Provider 已配置' : '本地意图 / Provider 关闭'));
        body.appendChild(row);
      });
      table.append(head, body);
      content.appendChild(table);
    } catch (_error) {
      content.setAttribute('role', 'alert');
      content.textContent = '真实回执读取失败，未使用 Mock 或历史缓存。';
    }
  }

  function start() {
    installQrFallback();
    setTimeout(mount, 0);
  }

  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', start, { once: true });
  else start();
})();
