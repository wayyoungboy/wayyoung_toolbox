export function escapeHTML(value = '') {
  return String(value).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]);
}

export const guides = {
  backup: { title: '备份与恢复', steps: '授权目录 → 新建任务 → 预览 → 备份', help: '源目录只读；挂载可见不代表云端上传完成。', action: '打开备份任务' },
  media: { title: '影片采集', steps: '订阅发现 → 下载 → 搬运 → 整理入库', help: '先核对目录与命名，再检查网络、下载器和待处理队列。', action: '打开影片工作台' },
  music: { title: '音乐转换', steps: '选择目录 → 扫描预览 → 开始转换', help: '先配置独立输出目录；默认保留原文件并跳过同名文件。', action: '设置转换目录' },
  integrity: { title: '文件完整性', steps: '选择目录 → 选择检查模式 → 查看报告', help: '检查只读文件；快速检查通过不等于完整内容无损。', action: '打开检查工具' },
  ha500: { title: 'HA500 硬件', steps: '确认硬件 → 验证通道 → 保存策略', help: '每个风扇独立设置。未验证的风扇保持监控，不自动接管。', action: '查看硬件状态' },
};
function requireFields(ok) { if (!ok) throw new Error('响应字段不完整，无法确认配置状态'); }
const complete = (summary, details = [], level = 'info') => ({ summary, details, level, checkedAt: new Date().toISOString() });

// Explicit read-only checks, never scanning trees, submitting tasks or granting access.
export async function checkModule(id, request) {
  const get = path => request(`/modules/${id}/api/${path}`);
  if (id === 'backup') {
    const [data, source, target] = await Promise.all([get('state'), get('directories?kind=source'), get('directories?kind=target')]);
    requireFields(Array.isArray(data.state?.tasks) && Array.isArray(data.state?.runs) && Array.isArray(source.roots) && Array.isArray(target.roots));
    const tasks = data.state.tasks, running = data.state.runs.filter(r => ['running', 'queued', 'cancel_requested'].includes(r.status)).length;
    return complete(tasks.length ? `${tasks.length} 个备份任务 · ${running} 个待执行/运行中` : '尚未创建备份任务', [
      `可选本地授权根目录 ${source.roots.length} 个；远程挂载 ${target.roots.length} 个。`,
      '请在新建任务中核对具体源目录授权与目标挂载；此检查不证明目标可写或云端完成。',
    ], !tasks.length || !target.roots.length || !source.roots.length ? 'warning' : 'info');
  }
  if (id === 'media') {
    const data = await get('pipeline');
    requireFields(Array.isArray(data.stages) && data.stages.length > 0 && typeof data.paths?.consistent === 'boolean');
    const warnings = data.stages.filter(s => ['error', 'warning'].includes(s.status));
    return complete(!data.paths.consistent ? '目录流程不一致，需要检查' : warnings.length ? `${warnings.length} 个环节需要关注` : '目录流程一致，连接检查已完成', [
      ...data.stages.map(s => `${s.label}：${s.summary}${s.error ? '（读取失败，请到模块查看详情）' : ''}`), ...(data.paths.issues || []),
    ], !data.paths.consistent || warnings.length ? 'warning' : 'info');
  }
  if (id === 'music') {
    const data = await get('settings');
    requireFields(typeof data.source_dir === 'string' && typeof data.output_dir === 'string');
    return complete(data.source_dir && data.output_dir ? '转换目录已配置' : '请先配置源目录与输出目录', [
      `源目录：${data.source_dir || '未设置'}`, `输出目录：${data.output_dir || '未设置'}`, '尚未扫描或转换；目录可读写性在预览/执行时校验。',
    ], data.source_dir && data.output_dir ? 'info' : 'warning');
  }
  if (id === 'ha500') {
    const data = await get('status');
    requireFields(typeof data.hardware?.supported === 'boolean' && Array.isArray(data.fans));
    const warning = !data.hardware.supported || !data.hardware.fan_driver || data.last_error || data.hardware.conflict;
    return complete(warning ? '硬件控制条件需要检查' : '硬件已识别，策略保持不变', [
      `风扇驱动：${data.hardware.fan_driver ? '可用' : '不可用'}`, ...data.fans.map(f => `${f.name}：${f.rpm} RPM · ${f.verified ? '已验证' : '未验证'} · ${f.policy?.mode === 'monitor' ? '仅监控' : '已有控制策略'}`),
      ...(data.last_error ? ['存在服务错误，请到运行日志查看。'] : []),
    ], warning ? 'warning' : 'info');
  }
  if (id === 'integrity') {
    const [config, status] = await Promise.all([get('config'), get('status')]);
    requireFields(Array.isArray(config.roots) && typeof status.running === 'boolean');
    return complete(status.running ? '正在检查文件' : '可按需进行只读检查', [`可选择 ${config.roots.length} 个存储根目录`, `上次/当前已检查 ${status.checked || 0} 个文件；发现损坏 ${status.damaged || 0} 个。`], status.last_error || status.damaged ? 'warning' : 'info');
  }
  throw new Error('此模块暂不提供配置检查');
}

export function overviewHTML(modules, checks, checking) {
  const needsAttention = m => Boolean(m.last_error || (m.enabled && !m.running) || ['warning', 'error'].includes(checks.get(m.id)?.level));
  const attention = modules.filter(needsAttention).length;
  const unchecked = modules.filter(m => m.running && !checks.has(m.id) && !needsAttention(m)).length;
  const disabled = modules.filter(m => !m.running && !m.enabled && !needsAttention(m)).length;
  const sorted = modules.slice().sort((a, b) => Number(needsAttention(b)) - Number(needsAttention(a)));
  return `<div class="overview-intro"><div><h2>今天从哪里开始？</h2><p>先检查配置，再进入工具。检查不会启动备份、扫描、下载或清理。</p></div><button class="primary-button" data-check-all ${checking.size || !modules.some(m => m.running) ? 'disabled' : ''}>${checking.size ? '正在检查…' : '检查配置与连接'}</button></div>
    <div class="overview-summary" aria-label="配置检查摘要"><span data-summary="attention" class="${attention ? 'attention' : ''}"><strong>${attention}</strong> 项需要处理</span><span data-summary="unchecked"><strong>${unchecked}</strong> 项尚未检查</span><span data-summary="disabled"><strong>${disabled}</strong> 项未启用</span><p>服务运行 ≠ 任务成功。以下只代表最近一次检查。</p></div>
    <div class="overview-list">${sorted.map(module => {
      const guide = guides[module.id] || { title: module.name, steps: '', help: '', action: '打开模块' };
      const check = checks.get(module.id);
      const state = module.last_error ? '服务异常' : module.running ? '服务运行' : module.enabled ? '启动异常' : '未启用';
      return `<article data-overview-module="${escapeHTML(module.id)}" class="overview-row ${escapeHTML(needsAttention(module) ? 'warning' : check?.level || '')}"><div class="overview-title"><h3>${escapeHTML(guide.title)}</h3><span class="overview-service">${escapeHTML(state)}</span><p>${escapeHTML(guide.steps)}</p></div>
        <div class="overview-detail"><strong>${escapeHTML(!module.running ? module.enabled ? '启动未完成，请查看模块错误' : '按需启用，不使用也可以保持关闭' : checking.has(module.id) ? '正在检查…' : check?.summary || '配置尚未检查')}</strong>
          <p>${escapeHTML(guide.help)}</p>${check ? `<details data-details="${escapeHTML(module.id)}"><summary>检查详情</summary><ul>${check.details.map(line => `<li>${escapeHTML(line)}</li>`).join('')}</ul><small>检查于 ${escapeHTML(new Date(check.checkedAt).toLocaleTimeString())}，仅代表当次结果</small></details>` : ''}
          ${module.last_error ? '<p class="error-text">模块操作失败，请打开模块查看错误，勿反复启停。</p>' : ''}</div>
        <div class="overview-actions"><button class="secondary-button" data-open="${escapeHTML(module.id)}">${escapeHTML(module.running ? guide.action : '查看模块')}</button><button class="text-button" data-check="${escapeHTML(module.id)}" ${!module.running || checking.has(module.id) ? 'disabled' : ''}>${checking.has(module.id) ? '检查中…' : check ? '重新检查' : '检查配置'}</button></div></article>`;
    }).join('')}</div><aside class="overview-note"><strong>配置和权限说明</strong><p>备份目录通过飞牛原生授权；其他文件工具由管理员选择存储卷内目录。硬件模块需要系统权限。不会自动扩大目录授权或开启新定时任务。</p><p>切换模块保留本次页面状态；刷新页面、关闭模块或应用升级仍会丢弃未保存内容，请先保存。</p></aside>`;
}
