import {useState} from 'react';

export default function ActorPortrait({actor}){
 const [failed,setFailed]=useState(false);
 const prefix=import.meta.env.BASE_URL.replace(/\/$/,'');
 return <div className="actor-portrait">{actor.has_portrait&&!failed?<img src={`${prefix}/api/catalog/actors/${encodeURIComponent(actor.id)}/portrait?v=${actor.portrait_revision}`} alt={actor.display_name} loading="lazy" onError={()=>setFailed(true)}/>:<span aria-hidden>人</span>}</div>;
}
