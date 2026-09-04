(function () {
  'use strict';
  const nativeFetch = window.fetch.bind(window);
  const radarMutation = /^\/api\/admin\/radar-links(?:\/\d+)?(?:\/(?:enable|disable))?$/;

  window.fetch = function (input, init) {
    const raw = typeof input === 'string' ? input : input instanceof URL ? input.pathname + input.search : input.url;
    const url = new URL(raw, location.origin);
    const method = String((init && init.method) || (input instanceof Request && input.method) || 'GET').toUpperCase();
    if (url.origin === location.origin && radarMutation.test(url.pathname) && ['POST', 'PUT', 'PATCH'].includes(method)) {
      const next = Object.assign({}, init || {});
      const headers = new Headers(next.headers || (input instanceof Request ? input.headers : undefined));
      if (!headers.has('Idempotency-Key')) headers.set('Idempotency-Key', 'radar-ui-' + crypto.randomUUID());
      next.headers = Object.fromEntries(headers.entries());
      const lifecycle = !url.pathname.endsWith('/enable') && !url.pathname.endsWith('/disable');
      const enabledToggle = lifecycle && document.querySelector('#swEnabled');
      const wantsEnabled = enabledToggle ? enabledToggle.classList.contains('on') : null;
      if (typeof next.body === 'string' && lifecycle) {
        try {
          const payload = JSON.parse(next.body);
          const toggle = document.querySelector('#swAuth');
          payload.auth_policy = toggle && !toggle.classList.contains('on') ? 'anonymous' : 'unionid_required';
          next.body = JSON.stringify(payload);
        } catch (_) { /* generated client owns body validation */ }
      }
      return nativeFetch(input, next).then(async function (response) {
        if (!response.ok || !lifecycle || wantsEnabled === null) return response;
        let value;
        try { value = await response.clone().json(); } catch (_) { return response; }
        const link = value && value.link;
        if (!link || !link.link_id) return response;
        const target = wantsEnabled ? 'enable' : 'disable';
        if ((wantsEnabled && link.status === 'enabled') || (!wantsEnabled && link.status !== 'enabled')) return response;
        const lifecycleHeaders = new Headers(headers);
        lifecycleHeaders.set('Content-Type', 'application/json');
        lifecycleHeaders.set('Idempotency-Key', 'radar-ui-' + crypto.randomUUID());
        return nativeFetch('/api/admin/radar-links/' + link.link_id + '/' + target, {
          method: 'POST', credentials: 'include', headers: Object.fromEntries(lifecycleHeaders.entries()),
          body: JSON.stringify({ expected_version: link.version })
        });
      });
    }
    return nativeFetch(input, init);
  };

  const hydrated = new Set();
  async function stats(id) {
    const response = await nativeFetch('/api/admin/radar-links/' + id + '/stats', { credentials: 'include' });
    if (!response.ok) throw new Error('stats unavailable');
    return response.json();
  }
  async function hydrateList() {
    const rows = document.querySelectorAll('#listRows tr');
    for (const row of rows) {
      const action = row.querySelector('[data-detail]');
      const id = action && Number(action.getAttribute('data-detail'));
      if (!id || hydrated.has('list-' + id)) continue;
      hydrated.add('list-' + id);
      try {
        const value = await stats(id);
        const nums = row.querySelectorAll('td.num');
        if (nums[0]) nums[0].textContent = Number(value.total_landings || 0).toLocaleString();
        if (nums[1]) nums[1].textContent = Number(value.authorized_users || 0).toLocaleString();
        if (nums[2]) nums[2].textContent = Number(value.view_opens || 0).toLocaleString();
        const cells = row.querySelectorAll('td');
        if (cells[6] && value.last_clicked_at) cells[6].textContent = String(value.last_clicked_at).slice(5, 16).replace('T', ' ');
      } catch (_) { hydrated.delete('list-' + id); }
    }
  }
  async function hydrateDetail() {
    if (document.body.dataset.page !== 'radarDetail' || hydrated.has('detail')) return;
    const id = Number(new URLSearchParams(location.search).get('id'));
    const nodes = document.querySelectorAll('.stat-row .stat-v');
    if (!id || nodes.length < 4) return;
    hydrated.add('detail');
    try {
      const value = await stats(id);
      const landings = Number(value.total_landings || 0), users = Number(value.authorized_users || 0);
      nodes[0].textContent = landings.toLocaleString(); nodes[1].textContent = users.toLocaleString();
      nodes[2].textContent = Number(value.view_opens || 0).toLocaleString(); nodes[3].textContent = landings ? Math.round(users / landings * 100) + '%' : '0%';
    } catch (_) { hydrated.delete('detail'); }
  }
  async function hydrateForm() {
    if (document.body.dataset.page !== 'radarForm' || hydrated.has('form')) return;
    const id = Number(new URLSearchParams(location.search).get('id'));
    const toggle = document.querySelector('#swAuth'); if (!id || !toggle) return;
    hydrated.add('form');
    try {
      const response = await nativeFetch('/api/admin/radar-links/' + id, { credentials: 'include' });
      const value = await response.json();
      if (response.ok && value.link && value.link.auth_policy === 'anonymous') toggle.classList.remove('on');
    } catch (_) { hydrated.delete('form'); }
  }
  function hydrate() { void hydrateList(); void hydrateDetail(); void hydrateForm(); }
  new MutationObserver(hydrate).observe(document.documentElement, { childList: true, subtree: true });
  document.addEventListener('DOMContentLoaded', hydrate);
})();
