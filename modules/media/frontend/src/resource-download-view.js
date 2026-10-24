// Never carry a caller-provided magnet, path or endpoint into a mutation.
export function resourceSelectionFromResult(result, hash, code, accepted) {
  if (!accepted || result?.code !== code || result.provider !== 'javdb' || result.state !== 'ready' || result.stale || !result.searched_at || !/^[a-f0-9]{40}$/.test(hash) || !result.items?.some(item => item.infohash === hash)) return null;
  return { code, provider: result.provider, infohash: hash, searched_at: result.searched_at };
}

export function resourceDestinationReady(config) {
  return Number.isSafeInteger(config?.revision) && config.revision >= 0 && typeof config.source_directory === 'string' && config.source_directory.startsWith('/') && config.source_directory !== '/' && !config.source_directory.split('/').includes('..');
}

export function resourceDownloadFailurePhase(failure) {
  return [400, 401, 403, 404, 409, 415, 422].includes(failure?.status) ? 'rejected' : 'unknown';
}

export function resourceJobDestination(job, config) {
  if (!job) return config?.source_directory || '尚未确认';
  if (job.resource?.download_path) return job.resource.download_path;
  if (typeof job.discovery?.download_path === 'string') return job.discovery.download_path || 'qB 默认目录（原任务未指定目录）';
  return job.intake_source_root || '已有任务，请前往任务详情核对';
}
