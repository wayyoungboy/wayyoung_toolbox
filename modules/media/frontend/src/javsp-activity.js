export function javspActivity(data) {
  const scanning = data?.discovery?.status === 'running';
  const runID = data?.discovery?.run_id;
  return {
    organizing: data?.organizing === true,
    scanning,
    canScan: data?.component?.available === true && !scanning,
    cancelScanRunID: scanning && Number.isSafeInteger(runID) && runID > 0 ? runID : null,
  };
}

export function javspTriggerLabel(trigger) {
  return { manual_scan: '手动扫描', schedule_scan: '定时扫描', queue: '队列整理', manual: '手动整理', schedule: '定时整理' }[trigger] || trigger || '未知';
}

export function javspRunOutput(run, data) {
  if (run.status === 'running') {
    if (['manual_scan', 'schedule_scan'].includes(run.trigger_type)) return run.output || '正在扫描文件并准备入队；此记录不是影片整理进度。';
    if (data.organizing) return data.live_output || run.output || '影片正在整理，等待引擎输出…';
    return run.output || '正在更新运行状态…';
  }
  return run.output || '没有输出';
}
