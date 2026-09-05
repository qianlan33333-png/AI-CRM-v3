(function () {
  'use strict';

  const platformFetch = typeof window.fetch === 'function' ? window.fetch.bind(window) : function () { return Promise.reject(new Error('fetch unavailable')); };
  function text(value) { return value == null ? '' : String(value); }
  function csrfToken() {
    const match = document.cookie.match(/(?:^|;\s*)(?:aicrm_admin_csrf|aicrm_csrf)=([^;]+)/);
    return match ? decodeURIComponent(match[1]) : '';
  }
  function requestKey(prefix) {
    return prefix + '-' + (crypto.randomUUID ? crypto.randomUUID() : Date.now().toString(36));
  }
  async function adminRequest(path, options) {
    const headers = new Headers((options && options.headers) || {});
    headers.set('X-CSRF-Token', csrfToken());
    headers.set('Idempotency-Key', requestKey('survey-admin'));
    const response = await platformFetch(path, Object.assign({ credentials: 'same-origin' }, options, { headers: headers }));
    if (!response.ok) {
      const error = new Error('HTTP ' + response.status);
      error.status = response.status;
      throw error;
    }
    return response.json();
  }
  function appendCell(row, value) {
    const cell = document.createElement('td');
    cell.textContent = text(value || '—');
    cell.style.cssText = 'padding:8px;border-bottom:1px solid #f2f3f5;vertical-align:top';
    row.appendChild(cell);
  }
  function statusLabel(item) {
    if (item.status === 'queued') return '等待发送';
    if (item.status === 'executed') return item.provider_result_received === true ? '已收到测试结果' : '已完成处理';
    if (item.status === 'outcome_unknown') return '结果待确认（不会自动重复发送）';
    if (item.status === 'disabled') return '当时未启用外推配置';
    if (item.status === 'legacy_success') return '历史记录：已完成';
    if (item.status === 'legacy_failed' || item.status === 'final_failed') return '未完成';
    if (item.status === 'attempted') return '正在等待结果';
    return '历史记录：' + text(item.status || '状态未知');
  }
  function attemptLabel(item) {
    const attempt = Number.isInteger(item.provider_attempt_number) ? item.provider_attempt_number : item.attempt_count;
    return Number.isInteger(attempt) ? '尝试 ' + attempt + ' 次' : '尝试次数未记录';
  }
  function makeRecords(title, items, marker) {
    const section = document.createElement('section');
    section.setAttribute('data-' + marker, 'true');
    const heading = document.createElement('h4'); heading.textContent = title; heading.style.cssText = 'margin:16px 0 6px;font-size:14px'; section.appendChild(heading);
    if (!Array.isArray(items) || items.length === 0) { const empty = document.createElement('p'); empty.textContent = '暂无测试记录。'; section.appendChild(empty); return section; }
    const table = document.createElement('table'); table.style.cssText = 'width:100%;border-collapse:collapse;font-size:12px';
    const head = document.createElement('thead'), headRow = document.createElement('tr');
    ['时间', '测试结果', '尝试情况', '备注'].forEach(function (label) { const cell = document.createElement('th'); cell.textContent = label; cell.style.cssText = 'padding:8px;text-align:left;border-bottom:1px solid #dee0e3'; headRow.appendChild(cell); });
    head.appendChild(headRow); const body = document.createElement('tbody');
    items.forEach(function (item) { const row = document.createElement('tr'); appendCell(row, item.occurred_at || item.updated_at || item.created_at); appendCell(row, statusLabel(item)); appendCell(row, attemptLabel(item)); appendCell(row, item.failure_category || (item.read_only_legacy ? '历史只读记录' : '—')); body.appendChild(row); });
    table.append(head, body); section.appendChild(table); return section;
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
        const alert = document.createElement('div'); alert.setAttribute('role', 'alert'); alert.dataset.surveyQrFallback = 'true'; alert.style.cssText = 'padding:16px;color:#d93026;text-align:center;line-height:1.6'; alert.textContent = '二维码加载失败，请使用上方“复制”按钮复制链接。'; box.replaceChildren(alert);
      }, 1200);
    }
    function scan() { if (document && document.body) watchQrBox(document.getElementById('shareQrBox')); }
    scan(); new MutationObserver(scan).observe(document.body, { childList: true, subtree: true });
  }
  async function mount() {
    if (document.body.dataset.page !== 'questionnaireOps') return;
    const questionnaireID = new URLSearchParams(location.search).get('id') || '';
    if (!/^[1-9][0-9]*$/.test(questionnaireID)) return;
    const stage = document.getElementById('stage');
    if (!stage || document.querySelector('[data-survey-canonical-receipts]')) return;
    const panel = document.createElement('section'); panel.dataset.surveyCanonicalReceipts = 'true'; panel.dataset.surveyHost = 'true'; panel.style.cssText = 'margin:16px 20px;padding:16px;background:#fff;border:1px solid #dee0e3;border-radius:8px';
    const heading = document.createElement('h3'); heading.textContent = '外推设置与测试'; heading.style.cssText = 'margin:0 0 6px;font-size:15px';
    const note = document.createElement('p'); note.textContent = '在这里设置外推参数，并查看本问卷和全部问卷的测试结果。'; note.style.cssText = 'margin:0 0 12px;color:#8f959e;font-size:12px';
    const content = document.createElement('div'); content.setAttribute('role', 'status'); content.textContent = '正在读取设置…'; panel.append(heading, note, content); stage.replaceChildren(panel);
    const operationsPath = '/api/admin/questionnaires/' + questionnaireID + '/operations';
    async function load() {
      const [payload, global] = await Promise.all([adminRequest(operationsPath, { method: 'GET', headers: { Accept: 'application/json' } }), adminRequest('/admin/questionnaires/external-push-logs?limit=100&offset=0', { method: 'GET', headers: { Accept: 'application/json' } })]);
      if (!payload || !Array.isArray(payload.items)) throw new Error('invalid operations');
      return { payload: payload, global: global && Array.isArray(global.items) ? global.items : [] };
    }
    function metadataForm(payload) {
      const push = payload.external_push || {}, metadata = push.metadata && typeof push.metadata === 'object' ? push.metadata : {};
      const form = document.createElement('form'); form.dataset.surveyPushMetadata = 'true'; form.style.cssText = 'display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:8px;margin:0 0 14px;padding:12px;border:1px solid #eef0f2;border-radius:6px';
      const enabled = document.createElement('label'); enabled.style.cssText = 'display:flex;gap:6px;align-items:center'; const enabledInput = document.createElement('input'); enabledInput.type = 'checkbox'; enabledInput.name = 'enabled'; enabledInput.checked = push.enabled === true; enabled.append(enabledInput, document.createTextNode('启用外推')); form.appendChild(enabled);
      const reference = document.createElement('label'); reference.textContent = '推送配置引用'; const referenceInput = document.createElement('input'); referenceInput.name = 'configuration_reference'; referenceInput.value = push.configuration_reference == null ? '' : String(push.configuration_reference); referenceInput.style.cssText = 'display:block;width:100%;box-sizing:border-box;margin-top:4px;height:28px;border:1px solid #dee0e3;border-radius:4px'; reference.appendChild(referenceInput); form.appendChild(reference);
      [['推送类型', 'type'], ['有效期秒时间戳', 'expires_at_ts'], ['第几天', 'day'], ['频次', 'frequency'], ['备注', 'remark']].forEach(function (item) { const label = document.createElement('label'); label.textContent = item[0]; const input = document.createElement('input'); input.name = item[1]; input.value = metadata[item[1]] == null ? '' : String(metadata[item[1]]); input.style.cssText = 'display:block;width:100%;box-sizing:border-box;margin-top:4px;height:28px;border:1px solid #dee0e3;border-radius:4px'; label.appendChild(input); form.appendChild(label); });
      const params = document.createElement('div'); params.dataset.surveyPushParams = 'true'; params.style.cssText = 'grid-column:1/-1;display:grid;gap:6px'; form.appendChild(params);
      function paramRow(name, value) { const row = document.createElement('div'); row.style.cssText = 'display:grid;grid-template-columns:1fr 1fr auto;gap:6px'; const key = document.createElement('input'), val = document.createElement('input'), remove = document.createElement('button'); key.dataset.paramName = 'true'; key.placeholder = '参数名'; key.value = name || ''; val.dataset.paramValue = 'true'; val.placeholder = '参数值'; val.value = value || ''; remove.type = 'button'; remove.textContent = '删除'; remove.onclick = function () { row.remove(); }; row.append(key, val, remove); params.appendChild(row); }
      Object.entries(metadata.custom_params || {}).forEach(function (item) { paramRow(item[0], item[1]); }); if (!params.children.length) paramRow('', '');
      const add = document.createElement('button'); add.type = 'button'; add.textContent = '添加自定义参数'; add.onclick = function () { paramRow('', ''); }; form.appendChild(add);
      const save = document.createElement('button'); save.type = 'submit'; save.textContent = '保存外推参数'; save.style.cssText = 'width:max-content;height:28px;border:0;border-radius:4px;background:#3370ff;color:#fff;padding:0 10px'; form.appendChild(save);
      form.addEventListener('submit', async function (event) {
        event.preventDefault();
        try {
          const next = { custom_params: {} }; ['type', 'expires_at_ts', 'day', 'frequency', 'remark'].forEach(function (key) { const value = form.elements[key].value.trim(); if (value) { const numeric = /^(day|frequency|expires_at_ts)$/.test(key), parsed = numeric ? Number(value) : value; if (numeric && (!Number.isFinite(parsed) || parsed < 0)) throw new Error('invalid metadata field'); next[key] = parsed; } });
          params.querySelectorAll('div').forEach(function (row) { const name = row.querySelector('[data-param-name]').value.trim(), value = row.querySelector('[data-param-value]').value; if (name) next.custom_params[name] = value; });
          if (!Number.isInteger(payload.configuration_version) || payload.configuration_version < 0) throw new Error('invalid configuration revision');
          const saved = await adminRequest(operationsPath + '/external-push', { method: 'PUT', headers: { 'Content-Type': 'application/json', Accept: 'application/json' }, body: JSON.stringify({ enabled: form.elements.enabled.checked, configuration_reference: form.elements.configuration_reference.value.trim(), metadata: next, configuration_version: payload.configuration_version }) });
          push.enabled = saved.external_push.enabled; push.configuration_reference = saved.external_push.configuration_reference; push.metadata = saved.external_push.metadata; save.textContent = '已保存';
        } catch (error) { save.textContent = error && (error.status === 409 || /\b409\b/.test(error.message || '')) ? '配置已更新，请重新打开后再保存' : '保存失败'; }
      });
      return form;
    }
    function testButton(reload) {
      const button = document.createElement('button'); button.type = 'button'; button.dataset.surveyPushTest = 'true'; button.textContent = '发送外推测试'; button.style.cssText = 'margin:0 8px 14px 0;width:max-content;height:28px;border:0;border-radius:4px;background:#3370ff;color:#fff;padding:0 10px';
      button.onclick = async function () { button.disabled = true; button.textContent = '正在创建测试…'; try { const receipt = await adminRequest(operationsPath + '/external-push/test', { method: 'POST', headers: { Accept: 'application/json' } }); button.textContent = receipt && receipt.status === 'queued' ? '测试已创建，等待结果' : '测试已创建'; await reload(); } catch (error) { button.textContent = error && error.status === 403 ? '保存失败：无操作权限' : '创建测试失败'; } finally { button.disabled = false; } };
      return button;
    }
    async function render() {
      const data = await load(); content.replaceChildren(metadataForm(data.payload), testButton(render), makeRecords('本问卷测试记录', data.payload.items, 'survey-test-records'), makeRecords('全部问卷测试记录', data.global, 'survey-global-test-records'));
    }
    try { await render(); } catch (_error) { content.setAttribute('role', 'alert'); content.textContent = '读取设置失败，请稍后重试。'; }
  }
  function installLegacyQuestionnaireOpsGuard() {
    if (document.body.dataset.page !== 'questionnaireOps') return;
    window.fetch = function (input, options) {
      const method = (options && options.method || (input && input.method) || 'GET').toUpperCase();
      const url = typeof input === 'string' ? input : input && input.url || '';
      // The donor controller only understands its historical queued-only log
      // projection. It may complete its frozen page boot with an empty list;
      // the Host replaces that view and reads the unmodified records itself.
      if (method === 'GET' && /^\/admin\/questionnaires\/[1-9][0-9]*\/external-push-logs(?:\?|$)/.test(url)) {
        return Promise.resolve({ok:true,status:200,json:function () { return Promise.resolve({items:[],total:0,limit:100,offset:0,has_more:false,local_only:true}); }});
      }
      return platformFetch(input, options);
    };
    document.addEventListener('click', function (event) {
      const button = event.target && event.target.closest && event.target.closest('button');
      if (button && button.textContent && button.textContent.includes('测试推送（仅本地记录）')) {
        event.preventDefault(); event.stopImmediatePropagation();
      }
    }, true);
  }
  function start() {
    installQrFallback();
    if (document.body.dataset.page !== 'questionnaireOps') { setTimeout(mount, 0); return; }
    const stage = document.getElementById('stage');
    if (!stage) return;
    let scheduled = false;
    const ensureHost = function () {
      if (scheduled || stage.querySelector('[data-survey-host]')) return;
      scheduled = true;
      setTimeout(function () { scheduled = false; if (!stage.querySelector('[data-survey-host]')) void mount(); }, 0);
    };
    new MutationObserver(ensureHost).observe(stage, { childList: true, subtree: true });
    ensureHost();
  }
  installLegacyQuestionnaireOpsGuard();
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', start, { once: true }); else start();
})();
