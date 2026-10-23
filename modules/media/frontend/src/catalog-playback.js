// One player owns one optimistic revision. Coalesce timeupdate events rather
// than racing writes, and stop after an uncertain result instead of overwriting
// a newer session's progress with blind retries.
export function createProgressSaver(initialRevision, write, onError = () => {}, sessionID = '') {
  let revision = initialRevision || 0;
  let next = null;
  let flight = null;
  let stopped = false;
  return {
    save(value) {
      if (stopped || !Number.isFinite(value.duration) || value.duration <= 0 || !Number.isFinite(value.position)) return flight || Promise.resolve();
      next = { position: Math.min(value.duration, Math.max(0, value.position)), duration: value.duration };
      if (!flight) {
        flight = Promise.resolve().then(async () => {
          try {
            while (next && !stopped) {
              const current = next;
              next = null;
              const result = await write({ ...current, revision, ...(sessionID ? {session_id:sessionID} : {}) });
              if (!Number.isSafeInteger(result.revision) || result.revision !== revision + 1) throw new Error('观看进度响应无效，请重新打开播放器');
              revision = result.revision;
            }
          } catch (error) {
            stopped = true;
            next = null;
            onError(error);
          } finally {
            flight = null;
          }
        });
      }
      return flight;
    },
  };
}

export function createPlaybackSession(){
 const bytes=new Uint8Array(16);
 globalThis.crypto.getRandomValues(bytes);
 return Array.from(bytes,value=>value.toString(16).padStart(2,'0')).join('');
}
export function playbackModeLabel(mode){return mode==='software'?'软件转码播放':mode==='auto'?'自动兼容播放':'原文件播放';}
export function loadedPlaybackProgress(video){
 if(!video||video.readyState<1||!Number.isFinite(video.duration)||video.duration<=0||!Number.isFinite(video.currentTime))return null;
 return {position:Math.min(video.duration,Math.max(0,video.currentTime)),duration:video.duration};
}

export function preferredAsset(assets = []) {
  return assets.filter(asset => asset.available).sort((a, b) => String(b.progress?.played_at || '').localeCompare(String(a.progress?.played_at || '')))[0] || null;
}

export function formatPlaybackTime(value) {
  const seconds = Number.isFinite(value) ? Math.max(0, Math.floor(value)) : 0;
  const hours = Math.floor(seconds / 3600);
  const minutes = Math.floor(seconds / 60) % 60;
  const remainder = String(seconds % 60).padStart(2, '0');
  return hours ? `${hours}:${String(minutes).padStart(2, '0')}:${remainder}` : `${minutes}:${remainder}`;
}

export function catalogAssetURL(id, kind = 'stream', subtitleIndex) {
  const suffix = kind === 'subtitles' ? `subtitles/${encodeURIComponent(subtitleIndex)}` : kind;
  return `${import.meta.env.BASE_URL.replace(/\/$/, '')}/api/catalog/assets/${encodeURIComponent(id)}/${suffix}`;
}
