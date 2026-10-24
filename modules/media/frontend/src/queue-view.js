export const queueStatuses = [
  ['pending', '排队中'], ['running', '运行中'], ['success', '成功'],
  ['partial', '部分成功'], ['failed', '失败'], ['canceled', '已取消'],
  ['timed_out', '已超时'], ['needs_attention', '待人工处理'], ['skipped', '已跳过'],
];
const statusLabels = Object.fromEntries(queueStatuses);
const phaseLabels = { discovered: '来源解析与投递', download: 'qB 下载', transfer: '完成搬运', qb_cleanup: 'qB 记录清理', organize: '影片整理', catalog: '馆藏登记', done: '已完成' };
export const queueStatusLabel = (status) => statusLabels[status] || status || '未知状态';
export const queuePhaseLabel = (job) => phaseLabels[job?.phase] || job?.phase || '未知阶段';
export const queueStatusTone = (status) => ({ success: 'success', failed: 'danger', partial: 'warning', timed_out: 'danger', needs_attention: 'warning', running: 'active' }[status] || 'neutral');

export function queueActions(job) {
  if (job?.archived || !Number.isSafeInteger(job?.version) || job.version < 1 || job.phase === 'done') return [];
  const actions = [];
  if (['partial', 'failed', 'canceled', 'timed_out', 'needs_attention'].includes(job.status)) {
    actions.push({ key: 'retry', label: job.phase === 'catalog' ? '重试馆藏登记' : job.phase === 'qb_cleanup' ? '重试 qB 清理' : job.phase === 'download' ? '恢复下载检查与后续处理' : '重试未完成步骤' });
  }
  if (['pending', 'running', 'partial', 'failed', 'timed_out', 'needs_attention'].includes(job.status)) {
    actions.push({ key: 'cancel', label: '取消任务' });
  }
  return actions;
}

export function queueActionPrompt(action, job) {
  if (action === 'retry' && job.phase === 'catalog') return `重试任务 #${job.id} 的馆藏登记？只核对已确认的输出，不会重新刮削或搬运。`;
  if (action === 'retry') return job.phase === 'download'
    ? `恢复任务 #${job.id} 的下载检查与后续处理？不会重新投递种子；完成后可由手动或定时入库继续搬运。`
    : `重试任务 #${job.id} 未完成的步骤？已完成步骤不会重新执行。`;
  return `取消任务 #${job.id}？不会停止 qB 下载，不会回滚已完成的文件操作，也不会删除视频。`;
}

export function queueSummary(job) {
  if (job.waiting_for_intake?.length) return `等待搬运任务 ${job.waiting_for_intake.map((id) => `#${id}`).join('、')} 完成整理登记`;
  if (job.error) return job.error;
  if (job.status === 'skipped') return job.related_job_id ? `已关联任务 #${job.related_job_id}` : '未重复处理';
  if (job.status === 'success' && job.kind === 'torrent') return '搬运及 qB 清理完成，不代表影片整理成功；请查看关联影片任务。';
  if (job.status === 'success') return job.catalog_registration ? `整理完成并已登记馆藏：${job.catalog_registration.work_count} 部作品、${job.catalog_registration.asset_count} 个文件` : '影片整理完成';
  if (job.phase === 'catalog' && job.status === 'pending') return '整理已完成，等待登记馆藏；不会重新刮削或搬运';
  if (job.phase === 'catalog' && job.status === 'running') return '正在核对已完成输出并登记馆藏';
  if (job.status === 'pending' && job.phase === 'download') {
    const reading = job.download;
    if (reading?.error) return `状态获取失败：${reading.error}；等待下次检查，尚未搬运`;
    if (reading?.observed_at && !reading.content_path) return '等待 qB 提供内容路径；尚未开始搬运';
    if (reading?.complete) return '下载完成，等待入库检查；尚未搬运或整理';
    const percent = downloadPercent(reading);
    return percent !== null ? `qB 已下载 ${percent}%；尚未开始搬运` : '等待下载完成；尚未开始搬运';
  }
  if (job.status === 'pending') return '等待工作协程，或等待其他文件操作结束';
  if (job.status === 'canceled') return '已停止后续处理，不会停止 qB 下载；已完成的文件操作不会回滚';
  if (job.status === 'running') return '正在执行当前阶段';
  return '请查看任务详情与处理记录';
}

export function downloadPercent(reading) {
  return reading?.observed_at && Number.isFinite(reading.progress) && reading.progress >= 0 && reading.progress <= 1
    ? Math.floor(reading.progress * 1000) / 10 : null;
}

export function downloadBytes(value) {
  if (!Number.isSafeInteger(value) || value < 0) return '未知';
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB'];
  let index = 0;
  while (value >= 1024 && index < units.length - 1) { value /= 1024; index++; }
  return `${Number(value.toFixed(1))} ${units[index]}`;
}

export function safeSourceURL(value) {
  try {
    const url = new URL(value);
    return ['http:', 'https:'].includes(url.protocol) && !url.username && !url.password ? url.href : '';
  } catch { return ''; }
}

export function queueEventLabel(reason) {
  return ({ enqueued: '已入队', claimed: '开始执行', retry: '手动重试', cancel: '取消任务', stage_result: '阶段结果',
    archived: '手动清理归档', catalog_registered: '已登记馆藏', intake_linked: '关联搬运来源', intake_movies_published: '影片任务已登记', intake_complete: '搬运及清理完成',
    download_complete: '下载完成', download_attention: '下载需人工处理', recovered: '重启恢复', unrecognized_input: '文件待识别' })[reason] || reason;
}

export function queueJobID(value) {
  const id = Number(value);
  return Number.isSafeInteger(id) && id > 0 ? id : null;
}

export async function loadQueueOverview(readQueue, signal) {
  const statuses = ['running', 'pending', 'needs_attention', 'failed', 'partial', 'timed_out'];
  const pages = await Promise.all(statuses.map((status) => readQueue({ status, limit: 5, signal })));
  const attentionPages = pages.slice(2);
  return {
    running: pages[0], pending: pages[1],
    attentionTotal: attentionPages.reduce((sum, page) => sum + page.total, 0),
    attention: [...new Map(attentionPages.flatMap((page) => page.jobs).map((job) => [job.id, job])).values()].slice(0, 8),
  };
}
