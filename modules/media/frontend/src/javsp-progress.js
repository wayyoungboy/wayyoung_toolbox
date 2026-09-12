const phaseLabels = {
  preparing: '准备运行',
  scanning: '扫描文件',
  processing: '整理影片',
  completed: '已完成',
  canceled: '已取消',
  failed: '执行失败',
};

export function javspProgressModel(progress = {}) {
  const current = Number.isFinite(progress.current) ? Math.max(0, progress.current) : 0;
  const total = Number.isFinite(progress.total) ? Math.max(0, progress.total) : 0;
  const processed = Number.isFinite(progress.processed) ? Math.max(0, progress.processed) : 0;
  const percentage = total > 0 ? Math.min(100, Math.round((current / total) * 100)) : 0;
  const details = [progress.movie, progress.site, progress.message].filter(Boolean);
  return {
    phaseLabel: phaseLabels[progress.phase] || '处理中',
    detail: details.join(' · '),
    countLabel: total > 0 ? `${current} / ${total}（已完成 ${processed}）` : '正在统计',
    percentage,
    terminal: ['completed', 'canceled', 'failed'].includes(progress.phase),
  };
}
