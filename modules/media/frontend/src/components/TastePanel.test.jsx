import {expect,it} from 'vitest';
import {renderToStaticMarkup} from 'react-dom/server';
import TastePanel from './TastePanel';
const report={config:{enabled:false,version:1,sources:{collection:true,history:true,feedback:true,follows:true}},sources:[{key:'collection',label:'馆藏与收藏',records:3,works:3}],facets:[],items:[],total:0};
it('offers opt-in without claiming remote discovery or browser collection',()=>{
 const html=renderToStaticMarkup(<TastePanel report={report} onSave={()=>{}} onClear={()=>{}} onOpen={()=>{}}/>);
 expect(html).toContain('启用本地偏好分析');
 expect(html).toContain('尚未启用');
 expect(html).toContain('不会联网');
 expect(html).toContain('保存分析设置');
 expect(html).not.toContain('自动订阅新作');
});
it('shows provenance and escapes untrusted metadata while loading locks mutations',()=>{
 const html=renderToStaticMarkup(<TastePanel busy report={{...report,config:{...report.config,enabled:true},facets:[{kind:'actor',key:'actor',label:'Test Actor',weight:6,evidence_count:1,sources:['follows']}],items:[{work:{id:'work',title:'<img src=x onerror=alert(1)>',code:'TEST-001',available_count:1,actors:[]},score:6,reasons:[{kind:'actor',label:'Test Actor',source:'follows',work_ids:[]}]}],total:1}} onSave={()=>{}} onClear={()=>{}} onOpen={()=>{}}/>);
 expect(html).toContain('本地演员关注');
 expect(html).toContain('排序分');
 expect(html).toContain('&lt;img');
 expect(html).not.toContain('<img src=x');
 expect(html).toMatch(/<button[^>]*disabled[^>]*>保存分析设置/);
 expect(html).toContain('参与分析：馆藏与收藏');
});
