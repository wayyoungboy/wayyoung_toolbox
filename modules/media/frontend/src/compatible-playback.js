// Only one creation may be in flight. Superseded responses are explicitly
// deleted, including those arriving after the player was closed.
export function createCompatiblePlayback({create,stop,onSession=()=>{}}){
 let generation=0,closed=false,current=null,sequence=Promise.resolve();
 const release=async()=>{if(current){const id=current;await stop(id);current=null;}};
 return {
  start(request){
   const expected=++generation;
   sequence=sequence.catch(()=>{}).then(async()=>{
    if(closed||expected!==generation)return null;
    await release();
    const session=await create(request);
    current=session.id;
    if(closed||expected!==generation){await release();return null;}
    onSession(session);return session;
   });return sequence;
  },
  close(){closed=true;generation++;sequence=sequence.catch(()=>{}).then(release);return sequence;},
 };
}
export function absolutePlaybackPosition(session,time){
 const position=session.offset+(Number.isFinite(time)?Math.max(0,time):0);
 return Math.min(session.duration,Math.max(0,position));
}
const timestamp=seconds=>{
 const ms=Math.max(0,Math.round(seconds*1000));
 return `${String(Math.floor(ms/3600000)).padStart(2,'0')}:${String(Math.floor(ms/60000)%60).padStart(2,'0')}:${String(Math.floor(ms/1000)%60).padStart(2,'0')}.${String(ms%1000).padStart(3,'0')}`;
};
const seconds=value=>value.split(':').reduce((sum,part)=>sum*60+Number(part),0);
export function shiftSubtitleText(text,offset){
 return text.replace(/\r\n/g,'\n').split(/\n\n+/).flatMap(block=>{
  const match=block.match(/((?:\d{2,}:)?\d{2}:\d{2}\.\d{3}) --> ((?:\d{2,}:)?\d{2}:\d{2}\.\d{3})/);
  if(!match)return [block];
  if(seconds(match[2])<=offset)return [];
  return [block.replace(match[0],`${timestamp(seconds(match[1])-offset)} --> ${timestamp(seconds(match[2])-offset)}`)];
 }).join('\n\n');
}
export function compatiblePlaylistURL(id){
 return `${import.meta.env.BASE_URL.replace(/\/$/,'')}/api/playback/sessions/${encodeURIComponent(id)}/index.m3u8`;
}
