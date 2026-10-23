import { useEffect, useMemo, useRef, useState } from 'react';
import Modal from '@douyinfe/semi-ui/lib/es/modal';
import { api } from '../api';
import { catalogAssetURL, createProgressSaver, formatPlaybackTime } from '../catalog-playback';

export default function CatalogPlayer({ asset, title, onClose }) {
  const videoRef = useRef(null);
  const lastSaved = useRef(0);
  const closed = useRef(false);
  const [error, setError] = useState('');
  const [saveError, setSaveError] = useState('');
  const [closing, setClosing] = useState(false);
  const saver = useMemo(() => createProgressSaver(asset.progress?.revision,
    value => api.catalogProgress(asset.id, value), failure => setSaveError(`观看进度未能保存：${failure.message}。请关闭后重新打开播放器。`)), [asset.id, asset.progress?.revision]);

  const save = () => {
    const video = videoRef.current;
    if (!video || video.error) return Promise.resolve();
    lastSaved.current = Date.now();
    return saver.save({ position: video.currentTime, duration: video.duration });
  };

  useEffect(() => {
    const video = videoRef.current;
    const flush = () => {
      if (video && !closed.current && !video.error) saver.save({ position: video.currentTime, duration: video.duration });
    };
    const hide = () => { if (document.visibilityState === 'hidden') flush(); };
    document.addEventListener('visibilitychange', hide);
    window.addEventListener('pagehide', flush);
    return () => {
      document.removeEventListener('visibilitychange', hide);
      window.removeEventListener('pagehide', flush);
      flush();
    };
  }, [saver]);

  const close = async () => {
    if (closing) return;
    setClosing(true);
    videoRef.current?.pause();
    await save();
    closed.current = true;
    onClose();
  };

  return <Modal title={title} visible width="min(1120px, calc(100vw - 24px))" className="catalog-player-modal" maskClosable={false} onCancel={close} footer={null}>
    <video ref={videoRef} controls autoPlay playsInline preload="metadata" aria-label={`播放 ${title}`}
      src={catalogAssetURL(asset.id)}
      onLoadedMetadata={() => {
        const video = videoRef.current;
        const position = asset.progress?.position || 0;
        if (video && position > 0 && !asset.progress?.completed && Number.isFinite(video.duration)) video.currentTime = Math.min(position, Math.max(0, video.duration - 1));
      }}
      onTimeUpdate={() => { if (Date.now() - lastSaved.current > 10000) save(); }}
      onPause={save} onEnded={save}
      onError={() => setError('当前浏览器无法播放此文件，或媒体文件已变化。请检查文件状态；本轮尚未接入转封装和转码。')}>
      {(asset.subtitles || []).map(subtitle => <track key={subtitle.index} kind="subtitles" src={catalogAssetURL(asset.id, 'subtitles', subtitle.index)} label={subtitle.label} srcLang={subtitle.language || 'und'} />)}
    </video>
    {error && <p className="catalog-alert" role="alert">{error}</p>}
    {saveError && <p className="catalog-alert" role="alert">{saveError}</p>}
    <div className="catalog-player-caption"><span>{asset.path}</span><span>{asset.progress?.position > 0 ? `上次看到 ${formatPlaybackTime(asset.progress.position)}` : '原文件播放'}{closing ? ' · 正在保存…' : ''}</span></div>
  </Modal>;
}
