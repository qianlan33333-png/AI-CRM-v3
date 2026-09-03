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
    note.textContent = '显示 v3 标准状态及历史结果。历史回执不可重放，生产 Provider 保持关闭。';
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
      if (!payload || payload.local_only !== true || payload.real_external_call_executed !== false || !Array.isArray(payload.items)) {
        throw new Error('invalid receipt contract');
      }
      content.replaceChildren();
      if (payload.items.length === 0) {
        content.textContent = '暂无外部操作回执。';
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
        appendCell(row, item.read_only_legacy ? '历史只读 / 不可重放' : '本地意图 / Provider 关闭');
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
