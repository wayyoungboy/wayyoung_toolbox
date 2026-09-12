const appPrefix = window.location.pathname.startsWith('/app/music_converter')
  ? '/app/music_converter'
  : '';

async function request(path, options = {}) {
  const response = await fetch(`${appPrefix}${path}`, {
    ...options,
    headers: {
      'Content-Type': 'application/json',
      ...(options.headers || {})
    }
  });
  const payload = await response.json().catch(() => ({}));
  if (!response.ok) {
    throw new Error(payload.error || `请求失败（${response.status}）`);
  }
  return payload;
}

export const api = {
  getHealth: () => request('/api/health'),
  getSettings: () => request('/api/settings'),
  saveSettings: (settings) => request('/api/settings', { method: 'PUT', body: JSON.stringify(settings) }),
  scan: () => request('/api/scan', { method: 'POST', body: '{}' }),
  startJob: () => request('/api/jobs', { method: 'POST', body: '{}' }),
  getCurrentJob: () => request('/api/jobs/current'),
  cancelJob: () => request('/api/jobs/current/cancel', { method: 'POST', body: '{}' }),
  getRuns: () => request('/api/runs'),
  clearRuns: () => request('/api/runs', { method: 'DELETE' }),
  getAbout: () => request('/api/about'),
  listDirectories: (path = '') => request(`/api/directories?path=${encodeURIComponent(path)}`)
};
