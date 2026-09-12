const BASE = location.pathname.replace(/\/$/, '');
const form = document.querySelector('#scan-form');
const pathInput = document.querySelector('#scan-path');
const modeSelect = document.querySelector('#scan-mode');
const startButton = document.querySelector('#start-scan');
const cancelButton = document.querySelector('#cancel-scan');
const resultsBody = document.querySelector('#results-body');
const loadMoreButton = document.querySelector('#load-more');

let currentStatus = {};
let resultFilter = '';
let resultOffset = 0;
let resultTotal = 0;
let loadingResults = false;
let timer = 0;
let startPending = false;
let cancelPending = false;
let lastRefreshError = '';
let statusReady = false;

async function api(path, options = {}) {
  const response = await fetch(`${BASE}${path}`, {
    ...options,
    headers: { 'Content-Type': 'application/json', ...(options.headers || {}) }
  });
  const contentType = response.headers.get('Content-Type') || '';
  const payload = contentType.includes('json') ? await response.json() : {};
  if (!response.ok) throw new Error(payload.error || `请求失败（HTTP ${response.status}）`);
  return payload;
}

function escapeHTML(value) {
  return String(value).replace(/[&<>"']/g, character => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[character]);
}

function showToast(message, error = false) {
  const node = document.querySelector('#toast');
  node.textContent = message;
  node.className = error ? 'toast error' : 'toast';
  node.hidden = false;
  clearTimeout(showToast.timeout);
  showToast.timeout = setTimeout(() => { node.hidden = true; }, 4500);
}

function showPathError(message) {
  const node = document.querySelector('#path-error');
  node.textContent = message;
  node.hidden = !message;
  pathInput.setAttribute('aria-invalid', message ? 'true' : 'false');
}

function formatBytes(bytes) {
  if (!Number.isFinite(bytes) || bytes < 1024) return `${bytes || 0} B`;
  const units = ['KB', 'MB', 'GB', 'TB'];
  let value = bytes;
  let index = -1;
  do { value /= 1024; index += 1; } while (value >= 1024 && index < units.length - 1);
  return `${value.toFixed(value >= 10 ? 1 : 2)} ${units[index]}`;
}

function statusLabel(status) {
  return ({ healthy: '正常', warning: '需复核', damaged: '疑似损坏', skipped: '已跳过' })[status] || status;
}

function phaseLabel(status) {
  if (status.running && status.phase === 'discovering') return '正在统计文件';
  if (status.running) return '正在检查';
  return ({ completed: '检查完成', cancelled: '已停止', failed: '检查失败', idle: '待检查' })[status.phase] || '待检查';
}

function renderStatus(status) {
  const previousStatus = currentStatus;
  currentStatus = status;
  const total = status.total || 0;
  const checked = status.checked || 0;
  const percent = total ? Math.min(100, Math.round(checked * 100 / total)) : 0;
  const phase = phaseLabel(status);
  const state = document.querySelector('#scan-state');
  state.textContent = phase;
  state.className = `state ${status.running ? 'running' : status.phase || 'idle'}`;
  document.querySelector('#progress-title').textContent = phase;
  document.querySelector('#progress-detail').textContent = status.root || '选择路径后开始检查';
  document.querySelector('#progress-percent').textContent = `${percent}%`;
  document.querySelector('#progress-bar').style.width = `${percent}%`;
  document.querySelector('#progress-track').setAttribute('aria-valuenow', String(percent));
  const currentMessage = status.current ? `当前：${status.current}` : (status.last_error || '');
  const currentPath = document.querySelector('#current-path');
  currentPath.textContent = currentMessage;
  currentPath.title = currentMessage;
  document.querySelector('#metric-checked').textContent = total ? `${checked} / ${total}` : checked;
  for (const key of ['healthy', 'warning', 'damaged', 'skipped']) {
    document.querySelector(`#metric-${key}`).textContent = status[key] || 0;
  }
  const controlsBusy = Boolean(status.running) || startPending;
  form.setAttribute('aria-busy', String(startPending || cancelPending));
  startButton.disabled = controlsBusy;
  startButton.textContent = startPending ? '正在启动…' : '开始检查';
  pathInput.disabled = controlsBusy;
  modeSelect.disabled = controlsBusy;
  cancelButton.hidden = !status.running;
  cancelButton.disabled = cancelPending;
  cancelButton.textContent = cancelPending ? '正在停止…' : '停止';
  document.querySelector('#downloads').hidden = !status.report_json;
  if (statusReady && previousStatus.running && !status.running && status.phase === 'completed') {
    showToast(`检查完成：${status.healthy || 0} 个正常，${status.warning || 0} 个需复核，${status.damaged || 0} 个疑似损坏`);
  }
  statusReady = true;
}

function resultRow(result) {
  const relative = currentStatus.root && result.path.startsWith(currentStatus.root)
    ? result.path.slice(currentStatus.root.length).replace(/^\//, '') || result.path
    : result.path;
  const checksum = result.checksum ? `<small title="SHA-256">${escapeHTML(result.checksum)}</small>` : '';
  return `<tr>
    <td data-label="状态"><span class="result-state ${escapeHTML(result.status)}">${escapeHTML(statusLabel(result.status))}</span></td>
    <td data-label="类型">${escapeHTML(result.kind)}</td>
    <td data-label="文件" class="file-cell" title="${escapeHTML(result.path)}"><strong>${escapeHTML(relative)}</strong>${checksum}</td>
    <td data-label="大小">${escapeHTML(formatBytes(result.size))}</td>
    <td data-label="说明" class="message-cell"><span>${escapeHTML(result.message)}</span><small>${escapeHTML(result.method)}</small></td>
  </tr>`;
}

async function loadResults({ append = false } = {}) {
  if (loadingResults) return;
  loadingResults = true;
  try {
    if (!append) resultOffset = 0;
    const payload = await api(`/api/results?status=${encodeURIComponent(resultFilter)}&offset=${resultOffset}&limit=200`);
    const rows = payload.results || [];
    resultTotal = payload.total || 0;
    if (append) resultsBody.insertAdjacentHTML('beforeend', rows.map(resultRow).join(''));
    else resultsBody.innerHTML = rows.length ? rows.map(resultRow).join('') : '<tr><td colspan="5" class="empty">当前分类没有结果</td></tr>';
    resultOffset += rows.length;
    document.querySelector('#result-count').textContent = `${resultTotal} 个结果`;
    loadMoreButton.hidden = resultOffset >= resultTotal;
  } catch (error) {
    showToast(error.message, true);
  } finally {
    loadingResults = false;
  }
}

async function refresh() {
  try {
    const wasRunning = Boolean(currentStatus.running);
    const previousChecked = currentStatus.checked || 0;
    const status = await api('/api/status');
    lastRefreshError = '';
    renderStatus(status);
    if (status.checked !== previousChecked || wasRunning !== status.running) await loadResults();
    clearTimeout(timer);
    timer = setTimeout(refresh, status.running ? 1200 : 5000);
  } catch (error) {
    if (error.message !== lastRefreshError) showToast(`状态更新失败：${error.message}`, true);
    lastRefreshError = error.message;
    clearTimeout(timer);
    timer = setTimeout(refresh, 5000);
  }
}

async function loadConfig() {
  try {
    const config = await api('/api/config');
    const hints = document.querySelector('#root-hints');
    const rootButtons = (config.roots || []).map(root => `<button type="button" data-root="${escapeHTML(root)}">${escapeHTML(root)}</button>`).join('');
    hints.innerHTML = `<span id="path-help">可检查的根目录：</span>${rootButtons || '<small>未配置可检查目录</small>'}`;
    if (!pathInput.value && config.roots?.length === 1) pathInput.value = config.roots[0];
  } catch (error) {
    showToast(error.message, true);
  }
}

form.addEventListener('submit', async event => {
  event.preventDefault();
  const path = pathInput.value.trim();
  if (!path.startsWith('/')) {
    showPathError('请输入以 / 开头的 NAS 绝对路径。');
    pathInput.focus();
    return;
  }
  showPathError('');
  startPending = true;
  renderStatus(currentStatus);
  try {
    await api('/api/scans', { method: 'POST', body: JSON.stringify({ path, mode: modeSelect.value }) });
    showToast('检查任务已启动');
    await refresh();
  } catch (error) {
    showToast(error.message, true);
  } finally {
    startPending = false;
    renderStatus(currentStatus);
  }
});

cancelButton.addEventListener('click', async () => {
  cancelPending = true;
  renderStatus(currentStatus);
  try {
    await api('/api/scans/current', { method: 'DELETE' });
    showToast('正在停止检查');
  } catch (error) {
    showToast(error.message, true);
  } finally {
    cancelPending = false;
    renderStatus(currentStatus);
  }
});

modeSelect.addEventListener('change', () => {
  document.querySelector('#mode-help').textContent = modeSelect.value === 'deep'
    ? '深度检查会完整读取并计算 SHA-256，同时调用可用的媒体、压缩包、PDF 和数据库校验器。大目录可能耗时数小时。'
    : '快速检查会读取文件头尾，并检查常见格式的基础结构。';
});

document.querySelector('#root-hints').addEventListener('click', event => {
  const button = event.target.closest('[data-root]');
  if (button) {
    pathInput.value = button.dataset.root;
    showPathError('');
    pathInput.focus();
  }
});

document.querySelector('#filters').addEventListener('click', event => {
  const button = event.target.closest('[data-status]');
  if (!button) return;
  resultFilter = button.dataset.status;
  document.querySelectorAll('#filters button').forEach(item => {
    const selected = item === button;
    item.classList.toggle('selected', selected);
    item.setAttribute('aria-pressed', String(selected));
  });
  loadResults();
});

pathInput.addEventListener('input', () => showPathError(''));
loadMoreButton.addEventListener('click', () => loadResults({ append: true }));
loadConfig();
refresh();
