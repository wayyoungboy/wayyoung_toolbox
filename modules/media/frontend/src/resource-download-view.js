// Never carry a caller-provided magnet, path or endpoint into a mutation.
export function resourceSelectionFromResult(result, hash, code, accepted) {
  if (!accepted || result?.code !== code || result.provider !== 'javdb' || result.state !== 'ready' || result.stale || !result.searched_at || !/^[a-f0-9]{40}$/.test(hash) || !result.items?.some(item => item.infohash === hash)) return null;
  return { code, provider: result.provider, infohash: hash, searched_at: result.searched_at };
}

export function resourceDestinationReady(config) {
  return Number.isSafeInteger(config?.revision) && config.revision >= 0 && typeof config.source_directory === 'string' && config.source_directory.startsWith('/') && config.source_directory !== '/' && !config.source_directory.split('/').includes('..');
}
