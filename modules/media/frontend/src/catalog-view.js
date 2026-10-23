const text=value=>typeof value==='string'?value.slice(0,500):'';
export function catalogPageOffset(offset,total){return Math.min(offset,Math.max(0,Math.floor((total-1)/48)*48));}
export function catalogView(initial={}){
 const offset=Number(initial.offset);
 return {query:text(initial.query),actor:text(initial.actor_name),tag:text(initial.tag),
  view:['all','continue','later','favorites','watched','unwatched'].includes(initial.view)?initial.view:'all',
  sort:['recent','title','year'].includes(initial.sort)?initial.sort:'recent',
  libraryID:text(initial.library),listID:text(initial.list),offset:Number.isSafeInteger(offset)&&offset>=0&&offset<=1000000?Math.floor(offset/48)*48:0};
}
export function catalogLocation(current,state,work=''){
 const url=new URL(current);
 const values={query:state.query,actor_name:state.actor,tag:state.tag,view:state.view==='all'?'':state.view,sort:state.sort==='recent'?'':state.sort,library:state.libraryID,list:state.listID,offset:state.offset?String(state.offset):'',work};
 for(const [key,value]of Object.entries(values)){if(value)url.searchParams.set(key,value);else url.searchParams.delete(key);}
 return url;
}
