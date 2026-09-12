const prefix = import.meta.env.BASE_URL.replace(/\/$/, '');

async function request(path, options = {}) {
  const response = await fetch(`${prefix}/api${path}`, {
    ...options,
    headers: {
      'Content-Type': 'application/json',
      ...(options.headers || {}),
    },
  });
  const data = await response.json().catch(() => ({}));
  if (!response.ok) {
    const error = new Error(data.error || `请求失败（${response.status}）`);
    error.status = response.status;
    throw error;
  }
  return data;
}

export const api = {
  queue: ({ status = '', before = 0, limit = 50, signal } = {}) => {
    const query = new URLSearchParams();
    if (status) query.set('status', status);
    if (before) query.set('before_id', String(before));
    query.set('limit', String(limit));
    return request(`/queue?${query}`, { signal });
  },
  queueJob: (id, { signal } = {}) => request(`/queue/${id}`, { signal }),
  retryQueueJob: (id, version) => request(`/queue/${id}/retry`, { method: 'POST', body: JSON.stringify({ version }) }),
  cancelQueueJob: (id, version) => request(`/queue/${id}/cancel`, { method: 'POST', body: JSON.stringify({ version }) }),
  health: () => request('/health'),
  dashboard: () => request('/dashboard'),
  pipeline: () => request('/pipeline'),
  tasks: () => request('/tasks'),
  createTask: (payload) => request('/tasks', { method: 'POST', body: JSON.stringify(payload) }),
  updateTask: (id, payload) => request(`/tasks/${id}`, { method: 'PUT', body: JSON.stringify(payload) }),
  deleteTask: (id) => request(`/tasks/${id}`, { method: 'DELETE' }),
  previewTask: (payload) => request('/tasks/preview', { method: 'POST', body: JSON.stringify(payload) }),
  runTask: (id, dryRun) => request(`/tasks/${id}/run`, {
    method: 'POST',
    body: JSON.stringify(dryRun === undefined ? {} : { dry_run: dryRun }),
  }),
  previewRunTask: (id) => request(`/tasks/${id}/run`, { method: 'POST', body: JSON.stringify({ dry_run: true }) }),
  submitTask: (id) => request(`/tasks/${id}/run`, { method: 'POST', body: JSON.stringify({ dry_run: false }) }),
  runs: (limit = 100) => request(`/runs?limit=${limit}`),
  unifiedRuns: ({ module = '', status = '', trigger = '', limit = 100 } = {}) => {
    const query = new URLSearchParams();
    if (module) query.set('module', module);
    if (status) query.set('status', status);
    if (trigger) query.set('trigger', trigger);
    query.set('limit', String(limit));
    return request(`/runs/unified?${query.toString()}`);
  },
  javsp: ({ signal } = {}) => request('/javsp', { signal }),
  javspDirectories: (path, { target, signal } = {}) => {
    const query = new URLSearchParams();
    if (path) query.set('path', path);
    if (target) query.set('target', target);
    return request(`/javsp/directories${query.size ? `?${query}` : ''}`, { signal });
  },
  previewJavspNames: (payload, { signal } = {}) => request('/javsp/naming-preview', { method: 'POST', body: JSON.stringify(payload), signal }),
  updateJavsp: (payload) => request('/javsp/config', { method: 'PUT', body: JSON.stringify(payload) }),
  runJavsp: () => request('/javsp/run', { method: 'POST', body: '{}' }),
  cancelJavsp: () => request('/javsp/cancel', { method: 'POST', body: '{}' }),
  cancelJavspDiscovery: (runID) => request('/javsp/discovery/cancel', { method: 'POST', body: JSON.stringify({ run_id: runID }) }),
  testJavsp: () => request('/javsp/test', { method: 'POST', body: '{}' }),
  anime: () => request('/anime'),
  animeDirectories: (path) => request(`/anime/directories${path ? `?path=${encodeURIComponent(path)}` : ''}`),
  updateAnime: (payload) => request('/anime/config', { method: 'PUT', body: JSON.stringify(payload) }),
  refreshAnimeTitles: () => request('/anime/titles/refresh', { method: 'POST', body: '{}' }),
  scanAnime: (directory) => request('/anime/scan', { method: 'POST', body: JSON.stringify({ directory }) }),
  writeAnimeNFO: (payload) => request('/anime/nfo', { method: 'POST', body: JSON.stringify(payload) }),
  maintenance: () => request('/maintenance'),
  maintenanceDirectories: (path) => request(`/maintenance/directories${path ? `?path=${encodeURIComponent(path)}` : ''}`),
  updateMaintenance: (payload) => request('/maintenance/config', { method: 'PUT', body: JSON.stringify(payload) }),
  scanDuplicates: (directory) => request('/maintenance/duplicates/scan', { method: 'POST', body: JSON.stringify({ directory }) }),
  deleteDuplicateVideos: (files) => request('/maintenance/duplicates/delete', { method: 'POST', body: JSON.stringify({ files }) }),
  previewEmptyDirectories: () => request('/maintenance/empty-directories/preview', { method: 'POST', body: '{}' }),
  cleanupEmptyDirectories: () => request('/maintenance/empty-directories/cleanup', { method: 'POST', body: '{}' }),
  intake: () => request('/intake'),
  intakeDirectories: (path) => request(`/intake/directories${path ? `?path=${encodeURIComponent(path)}` : ''}`),
  updateIntake: (payload) => request('/intake/config', { method: 'PUT', body: JSON.stringify(payload) }),
  previewIntake: (revision) => request('/intake/preview', { method: 'POST', body: JSON.stringify({ revision }) }),
  runIntake: (revision) => request('/intake/run', { method: 'POST', body: JSON.stringify({ revision }) }),
  settings: () => request('/settings'),
  mediaPaths: ({ signal } = {}) => request('/settings/paths', { signal }),
  previewMediaPaths: (payload, { signal } = {}) => request('/settings/paths/preview', { method: 'POST', body: JSON.stringify(payload), signal }),
  updateMediaPaths: (payload, { signal } = {}) => request('/settings/paths', { method: 'PUT', body: JSON.stringify(payload), signal }),
  updateSettings: (payload) => request('/settings', { method: 'PUT', body: JSON.stringify(payload) }),
  testQbittorrent: (payload) => request('/settings/test-qbittorrent', {
    method: 'POST',
    body: JSON.stringify(payload),
  }),
};

export function formatDate(value) {
  if (!value) return '--';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit',
  }).format(date);
}

export function formatDuration(start, finish) {
  if (!start || !finish) return '--';
  const seconds = Math.max(0, Math.round((new Date(finish) - new Date(start)) / 1000));
  if (seconds < 60) return `${seconds} 秒`;
  return `${Math.floor(seconds / 60)} 分 ${seconds % 60} 秒`;
}
