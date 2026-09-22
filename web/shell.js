import { APP_BASE, createRequest } from './api.js';
import { checkModule, escapeHTML, guides, overviewHTML } from './overview.js';

const presentation = { overview: ['总', '总览'], backup: ['备', '备份'], media: ['影', '影片'], music: ['乐', '音乐'], integrity: ['检', '检查'], ha500: ['硬', '硬件'] };
const order = Object.keys(presentation);

export function startToolbox({ request = createRequest(), doc = document, win = window } = {}) {
  const node = id => doc.getElementById(id);
  const events = new AbortController();
  const listen = (target, type, fn) => target.addEventListener(type, fn, { signal: events.signal });
  const frames = new Map(), checks = new Map(), checking = new Set();
  let modules = [], selected = 'overview', version = '', busy = '', disposed = false, refreshPromise, cancelDialog, activation = 0, acceptedState = '', connectionFailed = false;
  const pane = doc.createElement('div');
  pane.className = 'shell-pane';
  node('module-content').replaceChildren(pane);

  const stateOf = m => m.last_error ? ['运行异常', 'failed'] : m.running ? ['服务运行', 'running'] : m.enabled ? ['启动异常', 'failed'] : ['未启用', 'stopped'];
  const selection = () => {
    let hash;
    try { hash = decodeURIComponent(win.location.hash.slice(1)); } catch { return 'overview'; }
    return hash === 'overview' || modules.some(m => m.id === hash) ? hash : 'overview';
  };
  function notice(message = '', error = false) {
    node('notice').hidden = !message;
    node('notice').textContent = message;
    node('notice').className = error ? 'notice error' : 'notice';
  }
  function tabs() {
    const entries = [{ id: 'overview', name: '总览与配置' }, ...modules];
    const ids = entries.map(m => m.id).join(',');
    const nav = node('module-tabs');
    if (nav.dataset.ids !== ids) {
      nav.innerHTML = entries.map(m => `<button type="button" id="module-tab-${escapeHTML(m.id)}" class="module-tab module-${escapeHTML(m.id)}" data-module="${escapeHTML(m.id)}" role="tab" aria-controls="module-content"><span class="tab-icon">${presentation[m.id]?.[0] || '工'}</span><span class="tab-copy"><strong>${escapeHTML(m.name)}</strong><small></small></span><span class="tab-mobile-name">${presentation[m.id]?.[1] || escapeHTML(m.name)}</span><span class="tab-status" aria-hidden="true"></span></button>`).join('');
      nav.dataset.ids = ids;
    }
    for (const m of entries) {
      const button = node(`module-tab-${m.id}`), active = m.id === selected;
      const [label, state] = m.id === 'overview' ? ['配置、权限与使用步骤', ''] : stateOf(m);
      button.classList.toggle('selected', active);
      button.setAttribute('aria-selected', String(active));
      button.tabIndex = active ? 0 : -1;
      button.title = `${m.name} · ${label}`;
      button.querySelector('small').textContent = label;
      button.querySelector('.tab-status').className = `tab-status ${state}`;
    }
  }
  function reconcileFrames() {
    for (const [id, frame] of frames) {
      const m = modules.find(m => m.id === id);
      if (!m?.running || frame.dataset.generation !== (m.started_at || '')) {
        frame.remove(); frames.delete(id); checks.delete(id);
      }
    }
  }
  function openFrame(m) {
    let frame = frames.get(m.id);
    if (!frame) {
      frame = doc.createElement('iframe');
      frame.title = m.name;
      frame.dataset.generation = m.started_at || '';
      frame.src = `${APP_BASE}/modules/${encodeURIComponent(m.id)}/`;
      frame.className = 'module-frame';
      listen(frame, 'load', () => {
        // Presentation only. Business logic/authorization stay in the modules;
        // their standalone pages are unchanged.
        try {
          const child = frame.contentDocument;
          if (!child || child.getElementById('toolbox-embedded-style')) return;
          child.documentElement.dataset.toolboxModule = m.id;
          const style = child.createElement('link');
          style.id = 'toolbox-embedded-style'; style.rel = 'stylesheet'; style.href = `${APP_BASE}/embedded.css`;
          child.head.append(style);
        } catch { notice('模块页面未能完成初始化，请检查登录状态。', true); }
      });
      frames.set(m.id, frame);
      node('module-content').append(frame);
    }
    frame.hidden = false; frame.dataset.active = 'true'; frame.dataset.activation = String(activation);
  }
  function render() {
    if (disposed) return;
    reconcileFrames(); tabs();
    node('running-count').textContent = `${modules.filter(m => m.running).length} / ${modules.length} 服务运行`;
    node('toolbox-version').textContent = version ? `工具箱 v${version}` : '版本读取中';
    const m = modules.find(m => m.id === selected);
    const [label, state] = m ? stateOf(m) : ['统一工作台', ''];
    node('module-header').hidden = false;
    node('module-name').textContent = m?.name || '工具箱总览';
    node('module-version').textContent = m ? `组件 v${m.version}` : version ? `v${version}` : '—';
    node('module-description').textContent = m ? guides[m.id].steps : '配置检查与工具入口；不会自动执行任务。';
    node('module-icon').textContent = presentation[selected]?.[0] || '工';
    node('module-icon').className = `module-icon module-${selected}`;
    node('module-status').className = `status-pill ${state}`;
    node('module-status').textContent = label;
    node('module-controls').hidden = !m;
    node('module-toggle').checked = Boolean(m?.enabled);
    node('module-toggle').disabled = Boolean(busy);
    node('toggle-label').textContent = busy === selected ? '操作中…' : '模块服务';
    node('module-content').setAttribute('aria-labelledby', `module-tab-${selected}`);
    for (const [id, frame] of frames) { frame.hidden = id !== selected; frame.dataset.active = String(id === selected); }
    pane.hidden = Boolean(m?.running);
    if (!m) {
      const focused = doc.activeElement?.dataset;
      const expanded = [...pane.querySelectorAll('details[data-details][open]')].map(detail => detail.dataset.details);
      pane.className = 'shell-pane overview'; pane.innerHTML = overviewHTML(modules, checks, checking);
      for (const id of expanded) { const detail = pane.querySelector(`details[data-details="${id}"]`); if (detail) detail.open = true; }
      if (focused?.check) pane.querySelector(`[data-check="${focused.check}"]`)?.focus();
      else if (focused?.checkAll !== undefined) pane.querySelector('[data-check-all]')?.focus();
    } else if (m.running) openFrame(m);
    else {
      pane.className = 'shell-pane module-empty';
      pane.innerHTML = `<h2>${escapeHTML(label)}</h2><p>${m.enabled ? '启动未完成，请查看错误并检查配置。' : '启用模块后进入功能页面。原有配置与记录会保留。'}</p>${m.last_error ? `<pre>${escapeHTML(m.last_error)}</pre>` : ''}${!m.enabled ? `<button class="primary-button" data-enable="${escapeHTML(m.id)}" ${busy ? 'disabled' : ''}>启用此模块</button>` : ''}`;
    }
  }
  function accept(payload) {
    if (!Array.isArray(payload.modules) || payload.modules.some(m => !Object.hasOwn(presentation, m.id) || m.id === 'overview' || typeof m.running !== 'boolean')) throw new Error('模块状态响应无效');
    const snapshot = JSON.stringify([payload.modules, payload.version]);
    if (snapshot === acceptedState && !connectionFailed) return;
    acceptedState = snapshot;
    if (connectionFailed) { notice(); connectionFailed = false; }
    const previous = new Map(modules.map(m => [m.id, m.started_at]));
    modules = payload.modules.slice().sort((a, b) => order.indexOf(a.id) - order.indexOf(b.id));
    for (const m of modules) if (previous.get(m.id) !== m.started_at || !m.running) checks.delete(m.id);
    version = payload.version || version;
    selected = selection(); render();
  }
  function refresh() {
    if (disposed || busy) return Promise.resolve();
    if (refreshPromise) return refreshPromise;
    node('refresh').disabled = true;
    refreshPromise = request('/api/modules').then(payload => { if (!disposed) accept(payload); }).catch(error => {
      if (!disposed) {
        connectionFailed = true;
        notice(error.message, true);
        node('module-status').textContent = '连接异常';
        node('module-status').className = 'status-pill failed';
        if (!modules.length) { pane.textContent = '无法读取工具箱状态，请检查登录与服务后点击刷新。'; node('module-header').hidden = false; }
      }
    }).finally(() => { node('refresh').disabled = false; refreshPromise = undefined; });
    return refreshPromise;
  }
  function select(id) {
    if (id !== 'overview' && !modules.some(m => m.id === id)) return;
    if (selected !== id) activation++;
    selected = id; win.history.replaceState(null, '', `#${encodeURIComponent(id)}`); notice(); render();
  }
  function confirmStop(m) {
    const dialog = node('stop-dialog');
    node('stop-module-name').textContent = m.name;
    node('stop-hardware-warning').hidden = m.id !== 'ha500';
    dialog.showModal();
    return new Promise(resolve => {
      const finish = value => { dialog.close(); node('stop-confirm').onclick = null; node('stop-cancel').onclick = null; dialog.oncancel = null; cancelDialog = undefined; resolve(value); };
      cancelDialog = () => finish(false);
      node('stop-confirm').onclick = () => finish(true);
      node('stop-cancel').onclick = cancelDialog;
      dialog.oncancel = event => { event.preventDefault(); finish(false); };
    });
  }
  function confirmHardware() {
    const dialog = node('ha500-dialog'), input = node('ha500-confirmation');
    input.value = ''; node('ha500-error').hidden = true; dialog.showModal(); input.focus();
    return new Promise(resolve => {
      const finish = value => { dialog.close(); node('ha500-form').onsubmit = null; node('ha500-cancel').onclick = null; dialog.oncancel = null; cancelDialog = undefined; resolve(value); };
      cancelDialog = () => finish(false);
      node('ha500-form').onsubmit = event => {
        event.preventDefault();
        if (input.value.trim().toUpperCase() !== 'HA500') { node('ha500-error').textContent = '请输入 HA500 后再确认。'; node('ha500-error').hidden = false; return; }
        finish(true);
      };
      node('ha500-cancel').onclick = cancelDialog;
      dialog.oncancel = event => { event.preventDefault(); finish(false); };
    });
  }
  async function setEnabled(id, enabled) {
    const m = modules.find(m => m.id === id);
    if (busy || !m) return;
    busy = id; render();
    try {
      if (!enabled && !await confirmStop(m)) return;
      if (enabled && id === 'ha500' && !await confirmHardware()) return;
      if (disposed) return;
      const payload = await request(`/api/modules/${id}`, { method: 'PUT', timeoutMs: 45000, body: JSON.stringify({ enabled, confirmation: enabled && id === 'ha500' ? 'HA500' : '' }) });
      if (disposed) return;
      accept(payload); notice(enabled ? '模块已启用，原有计划将按配置运行。' : '模块已关闭，配置与记录保留。');
    } catch (error) { if (!disposed) notice(`${error.message} 请刷新核实状态。`, true); }
    finally { busy = ''; if (!disposed) { render(); await refresh(); } }
  }
  async function check(id) {
    const m = modules.find(m => m.id === id);
    if (!m?.running || checking.has(id)) return;
    checking.add(id); checks.delete(id); render();
    const generation = m.started_at;
    let result;
    try { result = await checkModule(id, request); }
    catch (error) { result = { summary: '检查失败，状态未知', details: [error.message], level: 'error', checkedAt: new Date().toISOString() }; }
    finally {
      checking.delete(id);
      if (!disposed) {
        const current = modules.find(m => m.id === id);
        if (current?.running && current.started_at === generation) checks.set(id, result);
        render();
      }
    }
  }
  listen(node('module-tabs'), 'click', event => { const id = event.target.closest('[data-module]')?.dataset.module; if (id) select(id); });
  listen(node('module-tabs'), 'keydown', event => {
    if (!['ArrowDown', 'ArrowUp', 'ArrowRight', 'ArrowLeft', 'Home', 'End'].includes(event.key)) return;
    const ids = ['overview', ...modules.map(m => m.id)];
    const i = event.key === 'Home' ? 0 : event.key === 'End' ? ids.length - 1 : (ids.indexOf(selected) + (['ArrowDown', 'ArrowRight'].includes(event.key) ? 1 : -1) + ids.length) % ids.length;
    event.preventDefault(); select(ids[i]); node(`module-tab-${ids[i]}`).focus();
  });
  listen(pane, 'click', event => {
    const button = event.target.closest('button');
    if (!button || button.disabled) return;
    if (button.dataset.open) select(button.dataset.open);
    else if (button.dataset.enable) void setEnabled(button.dataset.enable, true);
    else if (button.dataset.check) void check(button.dataset.check);
    else if (button.hasAttribute('data-check-all')) for (const m of modules) void check(m.id);
  });
  listen(node('module-toggle'), 'change', () => void setEnabled(selected, node('module-toggle').checked));
  listen(node('refresh'), 'click', () => void refresh());
  listen(win, 'hashchange', () => { const next = selection(); if (selected !== next) activation++; selected = next; render(); });
  const timer = setInterval(() => { if (!doc.hidden) void refresh(); }, 15000);
  const ready = refresh();
  return { ready, refresh, destroy() { disposed = true; clearInterval(timer); events.abort(); cancelDialog?.(); for (const frame of frames.values()) frame.remove(); } };
}
