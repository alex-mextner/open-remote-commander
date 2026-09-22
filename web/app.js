(() => {
  const $ = (id) => document.getElementById(id);
  const state = { base: '', token: '' };
  const els = {
    gateway: $('gateway'), token: $('token'), connect: $('connect'), disconnect: $('disconnect'),
    refresh: $('refresh'), devices: $('devices'), dot: $('gateway-dot'), status: $('gateway-status'),
    detail: $('gateway-detail'), pairCode: $('pair-code'), approve: $('approve'), pairMessage: $('pair-message')
  };

  const qs = new URLSearchParams(location.search);
  els.gateway.value = qs.get('gateway') || '';
  els.pairCode.value = qs.get('user_code') || '';

  function normalizeBase(v) {
    const u = new URL(v);
    if (u.protocol !== 'https:' && !(u.protocol === 'http:' && ['localhost','127.0.0.1','::1'].includes(u.hostname))) {
      throw new Error('Use HTTPS outside localhost.');
    }
    return u.origin + u.pathname.replace(/\/$/, '');
  }

  async function api(path, options = {}) {
    if (!state.base || !state.token) throw new Error('Connect to the gateway first.');
    const headers = new Headers(options.headers || {});
    headers.set('Authorization', `Bearer ${state.token}`);
    if (options.body && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json');
    const res = await fetch(state.base + path, { ...options, headers, cache: 'no-store' });
    if (!res.ok) {
      let message = `${res.status} ${res.statusText}`;
      try { const j = await res.json(); message = j?.error?.message || message; } catch {}
      throw new Error(message);
    }
    if (res.status === 204) return null;
    return res.json();
  }

  function setConnected(connected, detail = '') {
    els.dot.classList.toggle('online', connected);
    els.status.textContent = connected ? 'Gateway connected' : 'Gateway not connected';
    els.detail.textContent = detail || (connected ? state.base : 'Configure it below');
  }

  function escapeHTML(value) {
    return String(value).replace(/[&<>'"]/g, (c) => ({'&':'&amp;','<':'&lt;','>':'&gt;',"'":'&#39;','"':'&quot;'}[c]));
  }

  async function loadDevices() {
    els.refresh.disabled = true;
    try {
      const data = await api('/api/v1/devices');
      const devices = Array.isArray(data.devices) ? data.devices : [];
      if (!devices.length) {
        els.devices.className = 'devices empty';
        els.devices.innerHTML = '<p>No paired devices yet. Run <code>orc-agent</code> on a computer to start pairing.</p>';
        return;
      }
      els.devices.className = 'devices';
      els.devices.innerHTML = devices.map((d) => {
        const meta = d.online ? '<span class="online-label">Online</span>' : (d.last_seen_at ? `Last seen ${escapeHTML(new Date(d.last_seen_at).toLocaleString())}` : 'Offline');
        return `<div class="device" data-id="${escapeHTML(d.id)}"><div class="device-main"><div class="device-icon"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 5a2 2 0 0 1 2-2h12a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V5Zm4 16h8m-4-4v4"/></svg></div><div><div class="device-name">${escapeHTML(d.name)}</div><div class="device-meta">${meta} · ${escapeHTML(d.id)}</div></div></div><button class="revoke" data-revoke="${escapeHTML(d.id)}">Revoke</button></div>`;
      }).join('');
    } catch (err) {
      setConnected(false, err.message);
      els.devices.className = 'devices empty';
      els.devices.innerHTML = `<p>${escapeHTML(err.message)}</p>`;
    } finally { els.refresh.disabled = false; }
  }

  els.connect.addEventListener('click', async () => {
    try {
      state.base = normalizeBase(els.gateway.value.trim());
      state.token = els.token.value.trim();
      if (!state.token) throw new Error('Access token is required.');
      setConnected(true);
      await loadDevices();
    } catch (err) { state.base = ''; state.token = ''; setConnected(false, err.message); }
  });

  els.disconnect.addEventListener('click', () => {
    state.base = ''; state.token = ''; els.token.value = ''; setConnected(false);
    els.devices.className = 'devices empty'; els.devices.innerHTML = '<p>Connect to a gateway to load devices.</p>';
  });
  els.refresh.addEventListener('click', loadDevices);

  els.approve.addEventListener('click', async () => {
    els.pairMessage.className = 'message'; els.pairMessage.textContent = '';
    try {
      const code = els.pairCode.value.trim(); if (!code) throw new Error('Enter the pairing code.');
      await api('/api/v1/pairings/approve', { method: 'POST', body: JSON.stringify({ user_code: code }) });
      els.pairMessage.textContent = 'Device approved. The agent should connect in a moment.';
      await loadDevices();
    } catch (err) { els.pairMessage.className = 'message error'; els.pairMessage.textContent = err.message; }
  });

  els.devices.addEventListener('click', async (event) => {
    const button = event.target.closest('[data-revoke]'); if (!button) return;
    const id = button.getAttribute('data-revoke');
    if (!confirm('Revoke this device? It will need to be paired again.')) return;
    button.disabled = true;
    try { await api('/api/v1/devices/' + encodeURIComponent(id), { method: 'DELETE' }); await loadDevices(); }
    catch (err) { alert(err.message); button.disabled = false; }
  });
})();
