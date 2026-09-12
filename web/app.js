const APP_BASE = '/app/wayyoung_toolbox';
const tabsNode = document.querySelector('#module-tabs');
const contentNode = document.querySelector('#module-content');
const headerNode = document.querySelector('#module-header');
const noticeNode = document.querySelector('#notice');
const countNode = document.querySelector('#running-count');
const toggleNode = document.querySelector('#module-toggle');
const moduleNameNode = document.querySelector('#module-name');
const moduleVersionNode = document.querySelector('#module-version');
const moduleDescriptionNode = document.querySelector('#module-description');
const moduleStatusNode = document.querySelector('#module-status');
const moduleIconNode = document.querySelector('#module-icon');

const presentation = {
  backup: { short: '备', accent: '#3478f6', tint: '#eaf2ff' },
  ha500: { short: '硬', accent: '#e96b36', tint: '#fff0e9' },
  integrity: { short: '检', accent: '#0f8a63', tint: '#e6f7f1' },
  media: { short: '影', accent: '#7657d5', tint: '#f0edff' },
  music: { short: '乐', accent: '#1397a9', tint: '#e7f7f8' }
};

let modules = [];
let selectedID = '';
let busyID = '';
let frameID = '';

async function request(path, options = {}) {
  const response = await fetch(`${APP_BASE}${path}`, {
    ...options,
    headers: { 'Content-Type': 'application/json', ...(options.headers || {}) }
  });
  const payload = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(payload.error || `请求失败（HTTP ${response.status}）`);
  return payload;
}

function showNotice(message, error = false) {
  noticeNode.hidden = !message;
  noticeNode.textContent = message || '';
  noticeNode.className = error ? 'notice error' : 'notice';
}

function escapeHTML(value) {
  return String(value).replace(/[&<>"']/g, character => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[character]);
}

function moduleState(module) {
	if (module.running) return { label: '已启用', className: 'running' };
	if (module.enabled) return { label: '启用异常', className: 'failed' };
  return { label: '已关闭', className: 'stopped' };
}

function activeModule() {
  return modules.find(module => module.id === selectedID) || modules[0];
}

function renderTabs() {
  tabsNode.innerHTML = modules.map(module => {
    const style = presentation[module.id] || presentation.backup;
    const state = moduleState(module);
    const selected = module.id === selectedID;
    return `<button type="button" class="module-tab ${selected ? 'selected' : ''}" data-module="${escapeHTML(module.id)}" role="tab" aria-selected="${selected}" style="--accent:${style.accent};--tint:${style.tint}">
      <span class="tab-icon">${style.short}</span>
      <span class="tab-copy"><strong>${escapeHTML(module.name)}</strong><small>${escapeHTML(state.label)}</small></span>
      <span class="tab-status ${state.className}" aria-hidden="true"></span>
    </button>`;
  }).join('');
}

function renderContent(module) {
  if (module.running) {
    if (frameID === module.id && contentNode.querySelector('iframe')) return;
    frameID = module.id;
    contentNode.className = 'module-content embedded';
    contentNode.innerHTML = `<div class="frame-loading"><span class="spinner"></span><span>正在打开${escapeHTML(module.name)}…</span></div><iframe title="${escapeHTML(module.name)}" src="${APP_BASE}/modules/${encodeURIComponent(module.id)}/"></iframe>`;
    const frame = contentNode.querySelector('iframe');
    frame.addEventListener('load', () => contentNode.querySelector('.frame-loading')?.remove(), { once: true });
    return;
  }

  frameID = '';
  contentNode.className = 'module-content';
  const state = moduleState(module);
  const style = presentation[module.id] || presentation.backup;
  contentNode.innerHTML = `<div class="module-empty" style="--accent:${style.accent}">
    <div class="empty-symbol">${style.short}</div>
    <h2>${escapeHTML(state.label)}</h2>
		<p>${module.enabled ? '模块没有成功启用，请查看上方提示或重新切换开关。' : '这个模块当前不提供服务，需要时可独立启用。'}</p>
    ${module.last_error ? `<pre>${escapeHTML(module.last_error)}</pre>` : ''}
    ${module.enabled ? '' : '<button type="button" class="primary-button" id="enable-active">启用此模块</button>'}
  </div>`;
}

function render() {
  const module = activeModule();
	countNode.textContent = `单进程 · ${modules.filter(item => item.running).length} / ${modules.length} 个模块已启用`;
  if (!module) {
    headerNode.hidden = true;
    tabsNode.innerHTML = '<div class="nav-loading">没有可用模块</div>';
    contentNode.innerHTML = '<div class="initial-loading">没有可用模块</div>';
    return;
  }

  selectedID = module.id;
  renderTabs();
  const style = presentation[module.id] || presentation.backup;
  const state = moduleState(module);
  headerNode.hidden = false;
  moduleIconNode.textContent = style.short;
  moduleIconNode.style.setProperty('--accent', style.accent);
  moduleIconNode.style.setProperty('--tint', style.tint);
  moduleNameNode.textContent = module.name;
  moduleVersionNode.textContent = `v${module.version}`;
  moduleDescriptionNode.textContent = module.description;
  moduleStatusNode.className = `status-pill ${state.className}`;
  moduleStatusNode.innerHTML = `<span class="status-dot"></span><span>${escapeHTML(state.label)}</span>`;
  toggleNode.checked = module.enabled;
  toggleNode.disabled = busyID === module.id;
  renderContent(module);
}

function initialSelection(nextModules) {
  const hash = decodeURIComponent(location.hash.replace(/^#/, ''));
  if (nextModules.some(module => module.id === hash)) return hash;
  if (nextModules.some(module => module.id === selectedID)) return selectedID;
  return nextModules[0]?.id || '';
}

async function load({ quiet = false } = {}) {
  try {
    const payload = await request('/api/modules');
    const nextModules = payload.modules || [];
    selectedID = initialSelection(nextModules);
    modules = nextModules;
    render();
    if (!quiet) showNotice('');
  } catch (error) {
    showNotice(error.message, true);
  }
}

async function setEnabled(id, enabled) {
  let confirmation = '';
  if (enabled && id === 'ha500') {
    confirmation = window.prompt('硬件控制仅适用于奥睿科 HA500。请输入 HA500 确认型号：') || '';
    if (confirmation !== 'HA500') {
      showNotice('未确认 HA500 型号，硬件模块没有启动。', true);
      render();
      return;
    }
  }

  busyID = id;
  render();
  try {
    const payload = await request(`/api/modules/${encodeURIComponent(id)}`, {
      method: 'PUT', body: JSON.stringify({ enabled, confirmation })
    });
    modules = payload.modules || [];
		showNotice(enabled ? '模块已启用。' : '模块已关闭。');
  } catch (error) {
    showNotice(error.message, true);
  } finally {
    busyID = '';
    await load({ quiet: true });
  }
}

tabsNode.addEventListener('click', event => {
  const tab = event.target.closest('[data-module]');
  if (!tab || tab.dataset.module === selectedID) return;
  selectedID = tab.dataset.module;
  history.replaceState(null, '', `#${encodeURIComponent(selectedID)}`);
  showNotice('');
  render();
});

toggleNode.addEventListener('change', () => setEnabled(selectedID, toggleNode.checked));
contentNode.addEventListener('click', event => {
  if (event.target.closest('#enable-active')) setEnabled(selectedID, true);
});
document.querySelector('#refresh').addEventListener('click', () => load());
window.addEventListener('hashchange', () => {
  selectedID = initialSelection(modules);
  render();
});

load();
setInterval(() => load({ quiet: true }), 15000);
