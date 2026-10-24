export const primaryNavigation = [
  { key: 'catalog', label: '馆藏', group: 'watching' },
  { key: 'people', label: '人物', group: 'watching' },
  { key: 'personal', label: '个人记录', group: 'watching' },
  { key: 'dashboard', label: '工作台', group: 'primary' },
  { key: 'subscriptions', label: '发现与采集', group: 'primary' },
  { key: 'queue', label: '处理队列', group: 'primary' },
  { key: 'maintenance', label: '媒体维护', group: 'tools' },
  { key: 'settings', label: '设置', group: 'system' },
];

export const settingsTabs = [
  { key: 'paths', label: '目录与命名' },
  { key: 'general', label: '网络与服务' },
  { key: 'intake', label: '下载与入库' },
  { key: 'javsp', label: 'JAVSP 与翻译' },
  { key: 'diagnostics', label: '流程诊断' },
  { key: 'anime', label: '番剧工具（保留）' },
];

// Existing acquisition bookmarks remain reachable alongside the watching entry.
export function resolveDestination(page, tab = '') {
  if (page === 'resources') return { page: 'subscriptions', tab: 'resources' };
  if (page === 'tasks') page = 'subscriptions';
  if (page === 'runs') return { page: 'queue', tab: 'history' };
  if (['intake', 'javsp', 'anime'].includes(page)) return { page: 'settings', tab: page };
  if (page === 'pipeline') {
    if (tab === 'catalog') return { page: 'queue', tab: 'jobs' };
    if (tab === 'rss') return { page: 'subscriptions', tab: '' };
    return { page: 'settings', tab: ['intake', 'javsp'].includes(tab) ? tab : 'diagnostics' };
  }
  if (page === 'settings') return { page, tab: settingsTabs.some((item) => item.key === tab) ? tab : 'paths' };
  if (page === 'queue') return { page, tab: tab === 'history' ? tab : 'jobs' };
  if(page==='personal')return {page,tab:['lists','taste','sources'].includes(tab)?tab:'history'};
  if(page==='subscriptions')return {page,tab:tab==='resources'?tab:''};
  if (['catalog', 'people', 'dashboard', 'maintenance'].includes(page)) return { page, tab: '' };
  return { page: 'catalog', tab: '' };
}

export const pipelineTabs = [
  { key: 'overview', label: '流程概览' },
  { key: 'rss', label: 'RSS 来源' },
  { key: 'intake', label: '下载与入库' },
  { key: 'javsp', label: 'JAVSP 整理' },
];

const pipelineStageTabs = {
  rss: 'rss',
  qb: 'overview',
  intake: 'intake',
  javsp: 'javsp',
  catalog: 'catalog',
};

export function pipelineStageTab(stageKey) {
  return pipelineStageTabs[stageKey] || 'overview';
}

export function unifiedRunModeLabel(run) {
  if (run?.module !== 'rss' || typeof run.dry_run !== 'boolean') return '--';
  return run.dry_run ? '预览' : '正式';
}

export function pipelineFailureStages(message) {
  return [
    ['rss', 'RSS 发现'],
    ['qb', 'qBittorrent 下载'],
    ['intake', '下载完成搬运'],
    ['javsp', '刮削整理入库'],
    ['catalog', '馆藏登记'],
  ].map(([key, label]) => ({
    key,
    label,
    status: 'error',
    summary: '状态读取失败',
    detail: '',
    error: message,
  }));
}

export const runModuleOptions = [
  { value: '', label: '全部模块' },
  { value: 'rss', label: 'RSS 采集' },
  { value: 'intake', label: '下载入库' },
  { value: 'javsp', label: 'JAVSP 整理' },
  { value: 'maintenance', label: '媒体维护' },
];
