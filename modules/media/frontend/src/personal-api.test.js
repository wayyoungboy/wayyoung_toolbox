import { afterEach,expect,it,vi } from 'vitest';
import {api} from './api';
afterEach(()=>vi.unstubAllGlobals());
it('uses paginated reads and explicit CSRF/CAS mutations for profiles and records',async()=>{
 const fetch=vi.fn().mockImplementation(async url=>({ok:true,json:async()=>url.endsWith('/session')?{csrf:'csrf'}:{}}));vi.stubGlobal('fetch',fetch);
 await api.catalogActors({query:'Test & Actor',offset:48});
 await api.catalogActor('one');await api.catalogActorPatch('one',{version:1,display_name:'Actor',biography:'',aliases:[]});
 await api.catalogActorPortrait('one',{version:2,image:'data'});
 await api.catalogHistory({query:'TEST',offset:48});await api.catalogHistoryClear(['history']);
 await api.catalogLists();await api.catalogListCreate({name:'Weekend',description:''});
 await api.catalogListItems('list',3,['work']);await api.catalogListDelete('list',4);
 const writes=fetch.mock.calls.filter(([,options])=>options.method);
 expect(writes).toHaveLength(6);expect(writes.every(([,options])=>options.headers['X-Catalog-CSRF']==='csrf')).toBe(true);
 expect(fetch.mock.calls[0][0]).toContain('query=Test+%26+Actor');
 expect(fetch.mock.calls.some(([url])=>url.includes('/history?')&&url.includes('offset=48'))).toBe(true);
 expect(writes.find(([url])=>url.endsWith('/lists/list/items'))[1].body).toBe('{"version":3,"work_ids":["work"]}');
});
