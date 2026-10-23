import { useEffect, useMemo, useRef, useState } from 'react';
import Modal from '@douyinfe/semi-ui/lib/es/modal';
import { api } from '../api';
import { catalogAssetURL, createProgressSaver, createPlaybackSession, formatPlaybackTime } from '../catalog-playback';
import CompatiblePlayer from './CompatiblePlayer';

export default function CatalogPlayer({ asset, title, onClose }) {
  const videoRef = useRef(null);
  const lastSaved = useRef(0);
  const closed = useRef(false);
  const resume = useRef(asset.progress?.completed ? 0 : asset.progress?.position || 0);
  const flushRef = useRef(null), stopRef = useRef(null);
  const [mode,setMode]=useState('direct'),[quality,setQuality]=useState('1080p');
  const [changing,setChanging]=useState(false),[info,setInfo]=useState(null),[probing,setProbing]=useState(false),[caps,setCaps]=useState(null);
  const [attempt,setAttempt]=useState(0);
  const [error, setError] = useState('');
  const [saveError, setSaveError] = useState('');
  const [closing, setClosing] = useState(false);
  const [sessionID]=useState(createPlaybackSession);
  const saver = useMemo(() => createProgressSaver(asset.progress?.revision,
    value => api.catalogProgress(asset.id, value), failure => setSaveError(`观看进度未能保存：${failure.message}。请关闭后重新打开播放器。`),sessionID), [asset.id, asset.progress?.revision,sessionID]);

  const save = () => {
    if(mode!=='direct')return flushRef.current?.()||Promise.resolve();
    const video = videoRef.current;
    if (!video || video.error) return Promise.resolve();
    lastSaved.current = Date.now();
    if(Number.isFinite(video.currentTime))resume.current=video.currentTime;
    return saver.save({ position: video.currentTime, duration: video.duration });
  };
  const record=(value,force=false)=>{
    resume.current=value.position;
    if(force||Date.now()-lastSaved.current>10000){lastSaved.current=Date.now();return saver.save(value);}
  };
  const change=async(nextMode,nextQuality=quality)=>{
    if(changing||closing)return;setChanging(true);videoRef.current?.pause();await save();
    try{await stopRef.current?.();stopRef.current=null;setError('');setMode(nextMode);setQuality(nextQuality);}
    catch(failure){setError(`旧播放会话未能关闭：${failure.message}。请关闭后重试。`);}
    finally{setChanging(false);}
  };
  const probe=async()=>{
    setProbing(true);setError('');
    try{const [result,capabilities]=await Promise.all([api.playbackInfo(asset.id),api.playbackCapabilities()]);setInfo(result);setCaps(capabilities);}
    catch(failure){setError(failure.message);}finally{setProbing(false);}
  };

  useEffect(() => {
    api.prepareCatalogSession().catch(failure=>setSaveError(failure.message));
  }, []);
  useEffect(() => {
    const video = videoRef.current;
    const flush = () => {
      if(closed.current)return;
      if(mode!=='direct'){flushRef.current?.();return;}
      if (video && !video.error) saver.save({ position: video.currentTime, duration: video.duration });
    };
    const hide = () => { if (document.visibilityState === 'hidden') flush(); };
    document.addEventListener('visibilitychange', hide);
    const leave=()=>{flush();stopRef.current?.().catch(()=>{});};
    window.addEventListener('pagehide', leave);
    return () => {
      document.removeEventListener('visibilitychange', hide);
      window.removeEventListener('pagehide', leave);
      flush();
    };
  }, [saver,mode]);

  const close = async () => {
    if (closing) return;
    setClosing(true);
    videoRef.current?.pause();
    await save();
    try{await stopRef.current?.();}catch(failure){setError(`播放已停止，缓存清理未能确认：${failure.message}。无心跳会话会自动过期。`);}
    closed.current = true;
    onClose();
  };

  return <Modal title={title} visible width="min(1120px, calc(100vw - 24px))" className="catalog-player-modal" maskClosable={false} onCancel={close} footer={null}>
    <div className="catalog-playback-toolbar"><label>播放方式<select aria-label="播放方式" value={mode} disabled={changing||closing} onChange={event=>change(event.target.value)}><option value="direct">原文件播放</option><option value="auto">自动兼容</option><option value="software">软件转码</option></select></label><label>兼容画质<select aria-label="兼容画质" value={quality} disabled={mode==='direct'||changing||closing} onChange={event=>change(mode,event.target.value)}><option value="1080p">最高 1080p</option><option value="720p">最高 720p</option><option value="480p">最高 480p</option></select></label><button className="catalog-text-button" type="button" disabled={probing} onClick={probe}>{probing?'正在检测…':'媒体与编码信息'}</button></div>
    {info&&<p className="catalog-help">{info.video_codec.toUpperCase()} / {info.audio_codec?.toUpperCase()||'无音轨'} · {info.width}×{info.height} · {formatPlaybackTime(info.duration)} · {caps?.encoders?.some(value=>value.verified)?`已实际验证硬件编码：${caps.encoders.filter(value=>value.verified).map(value=>value.id).join('、')}`:'未验证可用硬件编码，兼容播放使用软件编码或转封装'}</p>}
    {mode==='direct'?<video key="direct" ref={videoRef} controls autoPlay playsInline preload="metadata" aria-label={`播放 ${title}`}
      src={catalogAssetURL(asset.id)}
      onLoadedMetadata={() => {
        const video = videoRef.current;
        const position = resume.current;
        if (video && position > 0 && Number.isFinite(video.duration)) video.currentTime = Math.min(position, Math.max(0, video.duration - 1));
      }}
      onTimeUpdate={() => { if (Date.now() - lastSaved.current > 10000) save(); }}
      onPause={save} onEnded={save}
      onError={() => setError('当前浏览器无法播放原文件，或文件已变化。可以选择“自动兼容”或“软件转码”；仍失败时请核对挂载和文件状态。')}>
      {(asset.subtitles || []).map(subtitle => <track key={subtitle.index} kind="subtitles" src={catalogAssetURL(asset.id, 'subtitles', subtitle.index)} label={subtitle.label} srcLang={subtitle.language || 'und'} />)}
    </video>:<CompatiblePlayer key={`${mode}:${quality}:${attempt}`} asset={asset} title={title} mode={mode} quality={quality} resume={resume} videoRef={videoRef} flushRef={flushRef} stopRef={stopRef} onProgress={record} onRestart={()=>{resume.current=0;setAttempt(value=>value+1);}}/>}
    {error && <p className="catalog-alert" role="alert">{error}</p>}
    {saveError && <p className="catalog-alert" role="alert">{saveError}</p>}
    <div className="catalog-player-caption"><span>{asset.path}</span><span>{asset.progress?.position > 0 ? `上次看到 ${formatPlaybackTime(asset.progress.position)}` : '原文件播放'}{closing ? ' · 正在保存…' : ''}</span></div>
  </Modal>;
}
