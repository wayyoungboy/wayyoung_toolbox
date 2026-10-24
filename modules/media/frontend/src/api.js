const prefix = import.meta.env.BASE_URL.replace(/\/$/, '');
let catalogToken='';
async function prepareCatalogSession(){const {csrf}=await request('/catalog/session');if(!csrf)throw new Error('馆藏请求校验失效，请刷新页面');catalogToken=csrf;return csrf;}

async function request(path, options = {}) {
  const response = await fetch(`${prefix}/api${path}`, {
    ...options,
    headers: {
      'Content-Type': 'application/json',
      ...(options.headers || {}),
    },
  });
  const data = await response.json().catch(() => { throw new Error('服务器返回结果不完整，请刷新核对；没有自动重试'); });
  if (!response.ok) {
    const error = new Error(data.error || `请求失败（${response.status}）`);
    error.status = response.status;
    throw error;
  }
  return data;
}

// Short-lived process token, fetched from an authenticated same-origin endpoint.
// A failed mutation is never replayed automatically (progress may have committed).
async function catalogMutation(path, method, payload, options = {}) {
  const csrf = options.keepalive&&catalogToken?catalogToken:await prepareCatalogSession();
  return request(path, { ...options, method, headers: { 'X-Catalog-CSRF': csrf }, body: JSON.stringify(payload) });
}
const playbackTokens=new Map();
async function playbackSessionMutation(id,action,payload){
 const csrf=playbackTokens.get(id)||(await request('/catalog/session')).csrf;
 if(!csrf)throw new Error('播放请求校验失效，请重新打开播放器');
 const result=await request(`/playback/sessions/${encodeURIComponent(id)}${action}`,{method:action?'POST':'DELETE',headers:{'X-Catalog-CSRF':csrf},body:JSON.stringify(payload),keepalive:!action});
 if(!action)playbackTokens.delete(id);
 return result;
}

export const api = {
  resourceCache: (code, { signal } = {}) => request(`/resources/search?${new URLSearchParams({ code, provider: 'javdb' })}`, { signal }),
  resourceSearch: (code, { signal } = {}) => catalogMutation('/resources/search', 'POST', { code, provider: 'javdb' }, { signal }),
  resourceDownloads: ({ signal } = {}) => request('/resources/downloads?limit=20', { signal }),
  resourceDownloadSelection: ({ code, provider, infohash }, { signal } = {}) => request(`/resources/downloads/selection?${new URLSearchParams({ code, provider, infohash })}`, { signal }),
  resourceDownload: ({ code, provider, infohash, searched_at }, revision, { signal } = {}) => catalogMutation('/resources/downloads', 'POST', { code, provider, infohash, searched_at, intake_revision: revision, confirmation: '下载资源' }, { signal }),
  catalogAutomation: ({ signal } = {}) => request('/catalog/automation', { signal }),
  catalogAutomationSave: payload => catalogMutation('/catalog/automation', 'PUT', payload),
  catalogBrowserSources: ({signal}={})=>request('/catalog/browser/sources',{signal}),
  catalogBrowserPreview: payload=>catalogMutation('/catalog/browser/import/preview','POST',payload),
  catalogBrowserImport: payload=>catalogMutation('/catalog/browser/import','POST',payload),
  catalogBrowserReading: (id,version,reading)=>catalogMutation(`/catalog/browser/sources/${encodeURIComponent(id)}`,'PUT',{version,reading}),
  catalogBrowserDelete: (id,version,confirmation)=>catalogMutation(`/catalog/browser/sources/${encodeURIComponent(id)}`,'DELETE',{version,confirmation}),
  catalogTaste: ({offset=0,signal}={})=>request(`/catalog/taste?${new URLSearchParams({limit:'24',offset:String(offset)})}`,{signal}),
  catalogTasteConfig: ({signal}={})=>request('/catalog/taste/config',{signal}),
  catalogTasteSave: payload=>catalogMutation('/catalog/taste/config','PUT',payload),
  catalogTasteReset: (version,confirmation)=>catalogMutation('/catalog/taste/reset','POST',{version,confirmation}),
  catalogActorFollow: (id,following,version)=>catalogMutation(`/catalog/actors/${encodeURIComponent(id)}/follow`,'PUT',{following,version}),
  prepareCatalogSession,
  playbackInfo: (id,{signal}={})=>request(`/catalog/assets/${encodeURIComponent(id)}/info`,{signal}),
  playbackResources: ()=>request('/playback/resources'),
  playbackCapabilities: ()=>catalogMutation('/playback/capabilities','POST',{}),
  playbackStart: async payload=>{
    const {csrf}=await request('/catalog/session');
    if(!csrf)throw new Error('播放请求校验失效，请重新打开播放器');
    const result=await request('/playback/sessions',{method:'POST',headers:{'X-Catalog-CSRF':csrf},body:JSON.stringify(payload)});
    playbackTokens.set(result.id,csrf);return result;
  },
  playbackStop: id=>playbackSessionMutation(id,'',{}),
  playbackPulse: (id,paused)=>playbackSessionMutation(id,'/heartbeat',{paused}),
  catalogActors: ({ query='', offset=0, following=false, signal }={}) => request(`/catalog/actors?${new URLSearchParams({query,offset:String(offset),limit:'48',following:String(following)})}`,{signal}),
  catalogActor: (id,{signal}={}) => request(`/catalog/actors/${encodeURIComponent(id)}`,{signal}),
  catalogActorPatch: (id,payload) => catalogMutation(`/catalog/actors/${encodeURIComponent(id)}`,'PATCH',payload),
  catalogActorPortrait: (id,payload) => catalogMutation(`/catalog/actors/${encodeURIComponent(id)}/portrait`,'PUT',payload),
  catalogActorPortraitDelete: (id,version) => catalogMutation(`/catalog/actors/${encodeURIComponent(id)}/portrait`,'DELETE',{version}),
  catalogHistory: ({query='',offset=0,signal}={}) => request(`/catalog/history?${new URLSearchParams({query,offset:String(offset),limit:'48'})}`,{signal}),
  catalogHistoryClear: (ids) => catalogMutation('/catalog/history/clear','POST',{ids}),
  catalogStats: ({signal}={}) => request('/catalog/personal/stats',{signal}),
  catalogLists: ({signal}={}) => request('/catalog/lists',{signal}),
  catalogList: (id,{signal}={}) => request(`/catalog/lists/${encodeURIComponent(id)}`,{signal}),
  catalogListCreate: (payload) => catalogMutation('/catalog/lists','POST',payload),
  catalogListUpdate: (id,payload) => catalogMutation(`/catalog/lists/${encodeURIComponent(id)}`,'PATCH',payload),
  catalogListItems: (id,version,workIDs) => catalogMutation(`/catalog/lists/${encodeURIComponent(id)}/items`,'PUT',{version,work_ids:workIDs}),
  catalogListDelete: (id,version) => catalogMutation(`/catalog/lists/${encodeURIComponent(id)}`,'DELETE',{version}),
  catalogDeletePreview: (workIDs) => catalogMutation('/catalog/deletion/preview', 'POST', { work_ids: workIDs }),
  catalogDeleteExecute: (workIDs, token, confirmation) => catalogMutation('/catalog/deletion/execute', 'POST', { work_ids: workIDs, token, confirmation }),
  queueCleanupCandidates: (days, { signal } = {}) => request(`/queue/cleanup?older_days=${encodeURIComponent(days)}`, { signal }),
  cleanupQueueJobs: (jobs) => catalogMutation('/queue/cleanup', 'POST', { jobs }),
  catalogLibraries: ({ signal } = {}) => request('/catalog/libraries', { signal }),
  catalogLibraryScans: (id, { signal } = {}) => request(`/catalog/libraries/${encodeURIComponent(id)}/scans?limit=1`, { signal }),
  catalogStartScan: id => catalogMutation(`/catalog/libraries/${encodeURIComponent(id)}/scans`, 'POST', {}),
  catalogScanAction: (id, version, action) => catalogMutation(`/catalog/scans/${encodeURIComponent(id)}/${encodeURIComponent(action)}`, 'POST', { version }),
  catalogAddLibrary: (payload) => catalogMutation('/catalog/libraries', 'POST', payload),
  catalogRelinkLibrary: (id, root) => catalogMutation(`/catalog/libraries/${encodeURIComponent(id)}`, 'PATCH', { root }),
  catalogScan: (id, { signal } = {}) => catalogMutation(`/catalog/libraries/${encodeURIComponent(id)}/scan`, 'POST', {}, { signal }),
  catalogWorks: ({ query = '', actor = '', actorID='', listID='', tag = '', sort = 'recent', view = 'all', libraryID = '', offset = 0, signal } = {}) => {
    const parameters = new URLSearchParams({ query, offset: String(offset), limit: '48' });
    if (libraryID) parameters.set('library_id', libraryID);
    if (actor) parameters.set('actor', actor);
    if (tag) parameters.set('tag', tag);
    if(actorID)parameters.set('actor_id',actorID);
    if(listID)parameters.set('list_id',listID);
    parameters.set('sort',sort);
    if (view === 'favorites') parameters.set('favorite', 'true');
    if (view === 'continue') parameters.set('continue', 'true');
    if (view === 'later') parameters.set('watch_later','true');
    if(view==='watched'||view==='unwatched')parameters.set('watched',String(view==='watched'));
    return request(`/catalog/works?${parameters}`, { signal });
  },
  catalogWork: (id, { signal } = {}) => request(`/catalog/works/${encodeURIComponent(id)}`, { signal }),
  catalogPersonal: (id, payload) => catalogMutation(`/catalog/works/${encodeURIComponent(id)}/personal`, 'PATCH', payload),
  catalogTitle: (id, title) => catalogMutation(`/catalog/works/${encodeURIComponent(id)}`, 'PATCH', { title }),
  catalogProgress: (id, payload) => catalogMutation(`/catalog/assets/${encodeURIComponent(id)}/progress`, 'PUT', payload, { keepalive: true }),
  completedLibrary: ({ query = '', offset = 0, limit = 50, signal } = {}) => request(`/library/completed?${new URLSearchParams({ query, offset: String(offset), limit: String(limit) })}`, { signal }),
  scanCompletedLibrary: ({ signal } = {}) => request('/library/completed/scan', { method: 'POST', body: '{}', signal }),
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
  previewAutomaticDuplicates: () => request('/maintenance/duplicates/preview', { method: 'POST', body: '{}' }),
  cleanupAutomaticDuplicates: () => request('/maintenance/duplicates/cleanup', { method: 'POST', body: '{}' }),
  previewOrphanFolders: () => request('/maintenance/orphan-folders/preview', { method: 'POST', body: '{}' }),
  cleanupOrphanFolders: () => request('/maintenance/orphan-folders/cleanup', { method: 'POST', body: '{}' }),
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
