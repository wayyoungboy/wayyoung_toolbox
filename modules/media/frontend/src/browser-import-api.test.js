import {afterEach,expect,it,vi} from 'vitest';
import {api} from './api';
import {resolveDestination} from './information-architecture';
afterEach(()=>vi.unstubAllGlobals());
it('keeps browser sources in the personal workspace and protects every write',async()=>{
 const fetch=vi.fn().mockImplementation(async url=>({ok:true,json:async()=>url.endsWith('/session')?{csrf:'browser-test-token'}:{items:[]}}));vi.stubGlobal('fetch',fetch);
 const signal=new AbortController().signal;
 await api.catalogBrowserSources({signal});
 const payload={name:'Neutral',kind:'history',filename:'history.json',data:'W10=',version:0};
 await api.catalogBrowserPreview(payload);await api.catalogBrowserImport({...payload,digest:'hash',confirmation:'导入资料'});
 await api.catalogBrowserReading('a/b',2,false);await api.catalogBrowserDelete('a/b',3,'删除来源');
 expect(resolveDestination('personal','sources')).toEqual({page:'personal',tab:'sources'});
 expect(fetch).toHaveBeenCalledWith('/api/catalog/browser/sources',expect.objectContaining({signal}));
 for(const [path,method,body] of [
  ['/import/preview','POST',payload],['/import','POST',{...payload,digest:'hash',confirmation:'导入资料'}],
  ['/sources/a%2Fb','PUT',{version:2,reading:false}],['/sources/a%2Fb','DELETE',{version:3,confirmation:'删除来源'}],
 ])expect(fetch).toHaveBeenCalledWith(`/api/catalog/browser${path}`,expect.objectContaining({method,body:JSON.stringify(body),headers:expect.objectContaining({'X-Catalog-CSRF':expect.any(String)})}));
});
