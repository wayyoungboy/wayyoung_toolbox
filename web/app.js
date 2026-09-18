import './directory-picker.js';

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
const toggleLabelNode = document.querySelector('#toggle-label');
const refreshNode = document.querySelector('#refresh');
const ha500Dialog = document.querySelector('#ha500-dialog');
const ha500Form = document.querySelector('#ha500-form');
const ha500Input = document.querySelector('#ha500-confirmation');
const ha500Error = document.querySelector('#ha500-error');

const presentation = {
  backup: { short: '备', mobile: '备份' },
  ha500: { short: '硬', mobile: '硬件' },
  integrity: { short: '检', mobile: '检查' },
  media: { short: '影', mobile: '影片' },
  music: { short: '乐', mobile: '音乐' }
};

let modules = [];
let selectedID = '';
let busyID = '';
let busyEnabled = false;
let frameID = '';
let loadPromise;

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
  clearTimeout(showNotice.timeout);
  noticeNode.hidden = !message;
  noticeNode.textContent = message || '';
  noticeNode.className = error ? 'notice error' : 'notice';
  if (message && !error) {
    showNotice.timeout = setTimeout(() => {
      noticeNode.hidden = true;
    }, 3200);
  }
}

function escapeHTML(value) {
  return String(value).replace(/[&<>"']/g, character => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[character]);
}

function themeClass(id) {
  return Object.hasOwn(presentation, id) ? `module-${id}` : 'module-backup';
}

function moduleState(module) {
  if (module.id === busyID) return { label: busyEnabled ? '正在启用…' : '正在关闭…', className: 'busy' };
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
    return `<button type="button" id="module-tab-${escapeHTML(module.id)}" class="module-tab ${themeClass(module.id)} ${selected ? 'selected' : ''}" data-module="${escapeHTML(module.id)}" role="tab" aria-selected="${selected}" aria-controls="module-content" tabindex="${selected ? '0' : '-1'}" title="${escapeHTML(`${module.name} · ${state.label}`)}">
      <span class="tab-icon">${style.short}</span>
      <span class="tab-copy"><strong>${escapeHTML(module.name)}</strong><small>${escapeHTML(state.label)}</small></span>
      <span class="tab-mobile-name">${escapeHTML(style.mobile || style.short)}</span>
      <span class="tab-assist">${escapeHTML(module.name)}，${escapeHTML(state.label)}</span>
      <span class="tab-status ${state.className}" aria-hidden="true"></span>
    </button>`;
  }).join('');
}

function renderContent(module) {
  const pending = module.id === busyID;
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
  contentNode.innerHTML = `<div class="module-empty ${themeClass(module.id)}">
    <div class="empty-symbol">${style.short}</div>
    <h2>${escapeHTML(state.label)}</h2>
    <p>${pending ? '请稍候，工具箱正在更新模块状态。' : (module.enabled ? '模块没有成功启用，请查看上方提示或重新切换开关。' : '这个模块当前不提供服务，需要时可独立启用。')}</p>
    ${module.last_error ? `<pre>${escapeHTML(module.last_error)}</pre>` : ''}
    ${module.enabled || pending ? '' : '<button type="button" class="primary-button" id="enable-active">启用此模块</button>'}
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
  moduleIconNode.className = `module-icon ${themeClass(module.id)}`;
  moduleNameNode.textContent = module.name;
  moduleVersionNode.textContent = `v${module.version}`;
  moduleDescriptionNode.textContent = module.description;
  moduleStatusNode.className = `status-pill ${state.className}`;
  moduleStatusNode.innerHTML = `<span class="status-dot"></span><span>${escapeHTML(state.label)}</span>`;
  const pending = busyID === module.id;
  toggleNode.checked = pending ? busyEnabled : module.enabled;
  toggleNode.disabled = busyID === module.id;
  toggleLabelNode.textContent = pending ? state.label : (module.enabled ? '关闭模块' : '启用模块');
  contentNode.setAttribute('aria-labelledby', `module-tab-${module.id}`);
  renderContent(module);
}

function initialSelection(nextModules) {
  const hash = decodeURIComponent(location.hash.replace(/^#/, ''));
  if (nextModules.some(module => module.id === hash)) return hash;
  if (nextModules.some(module => module.id === selectedID)) return selectedID;
  return nextModules[0]?.id || '';
}

function load({ quiet = false, fresh = false } = {}) {
  if (loadPromise) return fresh ? loadPromise.then(() => load({ quiet })) : loadPromise;
  refreshNode.disabled = true;
  refreshNode.classList.add('loading');
  loadPromise = (async () => {
    try {
      const payload = await request('/api/modules');
      const nextModules = payload.modules || [];
      selectedID = initialSelection(nextModules);
      modules = nextModules;
      render();
      if (!quiet) showNotice('');
    } catch (error) {
      showNotice(error.message, true);
    } finally {
      refreshNode.disabled = false;
      refreshNode.classList.remove('loading');
      loadPromise = undefined;
    }
  })();
  return loadPromise;
}

function confirmHA500() {
  ha500Input.value = '';
  ha500Error.hidden = true;
  ha500Dialog.showModal();
  requestAnimationFrame(() => ha500Input.focus());
  return new Promise(resolve => {
    const finish = approved => {
      ha500Dialog.close();
      ha500Form.onsubmit = null;
      document.querySelector('#ha500-cancel').onclick = null;
      ha500Dialog.oncancel = null;
      resolve(approved);
    };
    ha500Form.onsubmit = event => {
      event.preventDefault();
      if (ha500Input.value.trim().toUpperCase() !== 'HA500') {
        ha500Error.textContent = '请输入 HA500 后再确认。';
        ha500Error.hidden = false;
        ha500Input.focus();
        return;
      }
      finish(true);
    };
    document.querySelector('#ha500-cancel').onclick = () => finish(false);
    ha500Dialog.oncancel = event => {
      event.preventDefault();
      finish(false);
    };
  });
}

async function setEnabled(id, enabled) {
  let confirmation = '';
  if (enabled && id === 'ha500') {
    if (!await confirmHA500()) {
      render();
      return;
    }
    confirmation = 'HA500';
  }

  busyID = id;
  busyEnabled = enabled;
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
    await load({ quiet: true, fresh: true });
  }
}

function selectModule(id, { focus = false } = {}) {
  if (!modules.some(module => module.id === id)) return;
  selectedID = id;
  history.replaceState(null, '', `#${encodeURIComponent(selectedID)}`);
  showNotice('');
  render();
  if (focus) document.querySelector(`#module-tab-${id}`)?.focus();
}

tabsNode.addEventListener('click', event => {
  const tab = event.target.closest('[data-module]');
  if (!tab || tab.dataset.module === selectedID) return;
  selectModule(tab.dataset.module);
});

tabsNode.addEventListener('keydown', event => {
  if (!['ArrowDown', 'ArrowRight', 'ArrowUp', 'ArrowLeft', 'Home', 'End'].includes(event.key)) return;
  const ids = modules.map(module => module.id);
  let index = ids.indexOf(selectedID);
  if (event.key === 'Home') index = 0;
  else if (event.key === 'End') index = ids.length - 1;
  else index = (index + (['ArrowDown', 'ArrowRight'].includes(event.key) ? 1 : -1) + ids.length) % ids.length;
  event.preventDefault();
  selectModule(ids[index], { focus: true });
});

toggleNode.addEventListener('change', () => setEnabled(selectedID, toggleNode.checked));
contentNode.addEventListener('click', event => {
  if (event.target.closest('#enable-active')) setEnabled(selectedID, true);
});
refreshNode.addEventListener('click', () => load());
window.addEventListener('hashchange', () => {
  selectedID = initialSelection(modules);
  render();
});
load();
setInterval(() => load({ quiet: true }), 15000);
