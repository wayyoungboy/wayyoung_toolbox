import {useEffect,useRef,useState} from 'react';
import {api} from '../api';
import {catalogAssetURL,formatPlaybackTime} from '../catalog-playback';
import {absolutePlaybackPosition,compatiblePlaylistURL,createCompatiblePlayback,shiftSubtitleText} from '../compatible-playback';

function ShiftedSubtitles({asset,offset,onWarning}){
 const [tracks,setTracks]=useState([]);
 useEffect(()=>{
  const controller=new AbortController();const urls=[];
  Promise.allSettled((asset.subtitles||[]).map(async track=>{
   const response=await fetch(catalogAssetURL(asset.id,'subtitles',track.index),{signal:controller.signal});
   if(!response.ok)throw new Error('字幕读取失败');
   const text=await response.text();if(text.length>2<<20)throw new Error('字幕过大');
   if(controller.signal.aborted)return null;
   const url=URL.createObjectURL(new Blob([shiftSubtitleText(text,offset)],{type:'text/vtt'}));urls.push(url);
   return {...track,url};
  })).then(values=>{if(!controller.signal.aborted){setTracks(values.filter(value=>value.status==='fulfilled'&&value.value).map(value=>value.value));onWarning(values.some(value=>value.status==='rejected')?'部分字幕读取失败，其他字幕仍可选择。':'');}});
  return()=>{controller.abort();urls.forEach(url=>URL.revokeObjectURL(url));};
 },[asset,offset,onWarning]);
 return tracks.map(track=><track key={track.index} kind="subtitles" src={track.url} label={track.label} srcLang={track.language||'und'}/>);
}

export default function CompatiblePlayer({asset,title,mode,quality,resume,videoRef,flushRef,stopRef,onProgress,onRestart}){
 const [session,setSession]=useState(null),[loading,setLoading]=useState(true),[error,setError]=useState('');
 const [position,setPosition]=useState(resume.current),[target,setTarget]=useState(null);
 const [subtitleWarning,setSubtitleWarning]=useState('');
 const [finished,setFinished]=useState(false);
 const controllerRef=useRef(null),sessionRef=useRef(null),pulseChain=useRef(Promise.resolve()),heartbeatStopped=useRef(false);
 const mediaCleanup=useRef(null);
 const save=()=>{
  const view=sessionRef.current,video=videoRef.current;
  if(view&&video){const position=video.readyState>0?absolutePlaybackPosition(view,video.currentTime):resume.current;return onProgress({position,duration:view.duration},true);}
  return Promise.resolve();
 };
 const fail=message=>{
  save();heartbeatStopped.current=true;setError(message);videoRef.current?.pause();mediaCleanup.current?.();sessionRef.current=null;setSession(null);controllerRef.current?.close().catch(()=>{});
 };
 const finish=async()=>{await save();heartbeatStopped.current=true;mediaCleanup.current?.();sessionRef.current=null;setSession(null);await controllerRef.current?.close().catch(()=>{});setFinished(true);};
 const pulse=()=>{
  const view=sessionRef.current;if(!view||heartbeatStopped.current)return;
  const paused=videoRef.current?.paused??true;
  pulseChain.current=pulseChain.current.catch(()=>{}).then(()=>api.playbackPulse(view.id,paused)).then(result=>{
   if(sessionRef.current?.id===view.id&&(result.state==='failed'||result.state==='stopped'))throw new Error('转码进程已停止，请选择软件转码或重新打开播放器');
  }).catch(failure=>{if(sessionRef.current?.id===view.id)fail(failure.message);});
 };
 useEffect(()=>{
  let disposed=false;
  const controller=createCompatiblePlayback({create:api.playbackStart,stop:api.playbackStop,onSession:value=>{
   if(!disposed){sessionRef.current=value;resume.current=value.offset+value.seek;setSession(value);setPosition(value.offset+value.seek);setLoading(false);}
  }});
  controllerRef.current=controller;stopRef.current=()=>controller.close();
  controller.start({asset_id:asset.id,mode,quality,start:resume.current}).catch(failure=>{if(!disposed){setError(failure.message);setLoading(false);}});
  const heartbeat=setInterval(pulse,15000);
  return()=>{disposed=true;clearInterval(heartbeat);save();sessionRef.current=null;controller.close().catch(()=>{});};
 // Player is keyed by mode/quality; the mutable refs own the current timeline.
 // eslint-disable-next-line react-hooks/exhaustive-deps
 },[]);
 useEffect(()=>{
  if(!session)return;
  let disposed=false,hls=null;
  const video=videoRef.current;
  const source=compatiblePlaylistURL(session.id);
  const failed=()=>{if(!disposed)fail('兼容播放中断，会话已停止。请选择软件转码或关闭后重新播放。没有自动重试。');};
  const play=()=>video.play().catch(()=>{});
  import('hls.js/dist/hls.light.mjs').then(({default:Hls})=>{
   if(disposed)return;
   // Chromium may advertise native HLS but reject MPEG-TS segments. Prefer
   // tested MediaSource transmuxing; Safari without MSE can still use native HLS.
   if(!Hls.isSupported()){
    if(video.canPlayType('application/vnd.apple.mpegurl')){video.src=source;video.addEventListener('loadedmetadata',play,{once:true});return;}
    failed();return;
   }
   hls=new Hls({startPosition:session.seek,maxBufferLength:12,maxMaxBufferLength:20,backBufferLength:4,liveSyncDurationCount:12,liveMaxLatencyDurationCount:20,maxLiveSyncPlaybackRate:1});
   hls.on(Hls.Events.ERROR,(_,data)=>{if(data.fatal)failed();});
   hls.on(Hls.Events.MANIFEST_PARSED,play);hls.loadSource(source);hls.attachMedia(video);
  }).catch(failed);
  const cleanup=()=>{if(disposed)return;disposed=true;hls?.destroy();video.pause();video.removeAttribute('src');video.load();video.removeEventListener('loadedmetadata',play);};
  mediaCleanup.current=cleanup;
  return()=>{cleanup();if(mediaCleanup.current===cleanup)mediaCleanup.current=null;};
 // The keyed player owns one controller and the callbacks read its current refs.
 // eslint-disable-next-line react-hooks/exhaustive-deps
 },[session,videoRef]);
 flushRef.current=save;
 const seek=()=>{
  if(target===null||!session||loading)return;
  const start=Math.min(Math.max(0,target),Math.max(0,session.duration-1));
  save();setLoading(true);setError('');sessionRef.current=null;setSession(null);setTarget(null);
  controllerRef.current.start({asset_id:asset.id,mode,quality,start}).catch(failure=>{setLoading(false);setError(failure.message);});
 };
 return <div className="compatible-player">
  <video key={session?.id||'preparing'} ref={videoRef} controls playsInline aria-label={`播放 ${title}`} onTimeUpdate={()=>{
   if(!session)return;const position=absolutePlaybackPosition(session,videoRef.current.currentTime);setPosition(position);onProgress({position,duration:session.duration});
  }} onLoadedMetadata={()=>{if(session&&videoRef.current.canPlayType('application/vnd.apple.mpegurl'))videoRef.current.currentTime=session.seek;}}
   onPause={()=>{save();pulse();}} onPlay={pulse} onEnded={finish} onError={()=>{if(session)fail('当前兼容流无法播放，会话已停止，请切换播放方式后重试。');}}>
   {session&&<ShiftedSubtitles key={session.id} asset={asset} offset={session.offset} onWarning={setSubtitleWarning}/>}
  </video>
  {loading&&<p role="status">正在准备兼容播放，首次转码可能需要数秒…</p>}
  {session&&<><div className="compatible-timeline"><label htmlFor="movie-position">完整影片进度</label><output>{formatPlaybackTime(target??position)} / {formatPlaybackTime(session.duration)}</output><input id="movie-position" type="range" aria-label="完整影片进度" min="0" max={Math.max(0,session.duration-1)} step="1" value={target??position} disabled={loading||!!error} onChange={event=>setTarget(Number(event.target.value))} onPointerUp={seek} onKeyUp={event=>{if(['ArrowLeft','ArrowRight','Home','End'].includes(event.key))seek();}}/><button type="button" disabled={loading||!!error||target===null} onClick={seek}>跳转到此处</button></div>
   <p className="catalog-help" role="status">{session.mode==='remux'?'转封装 · 视频保持原编码':`转码 · ${session.backend==='libx264'?'CPU 软件编码':session.backend}`} · {quality}{session.fallback?' · 硬件启动失败，已安全回退软件编码':''}。暂停会暂停转码；跳转将重建会话。</p></>}
  {error&&<p className="catalog-alert" role="alert">{error}</p>}
  {subtitleWarning&&<p className="catalog-alert" role="alert">{subtitleWarning}</p>}
  {finished&&<p className="catalog-notice">播放结束，转码会话已回收。 <button type="button" className="catalog-text-button" onClick={onRestart}>从头重新播放</button></p>}
 </div>;
}
