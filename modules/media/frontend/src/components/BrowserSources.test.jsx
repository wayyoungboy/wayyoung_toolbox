import {expect,it} from 'vitest';
import {renderToStaticMarkup} from 'react-dom/server';
import {BrowserSourceList,BrowserImportForm} from '../pages/BrowserSources';
it('distinguishes retained stop from permanent source deletion and escapes labels',()=>{
 const html=renderToStaticMarkup(<BrowserSourceList items={[{id:'source',name:'<script>untrusted</script>',kind:'history',records:4,matched:1,reading:false,version:3}]} busy={false} onReading={()=>{}} onDelete={()=>{}}/>);
 expect(html).toContain('停止读取仍保留');expect(html).toContain('恢复读取');expect(html).toContain('删除来源');expect(html).toContain('&lt;script&gt;');expect(html).not.toContain('<script>');
});
it('requires a preview before publication and does not claim direct browser access',()=>{
 const html=renderToStaticMarkup(<BrowserImportForm items={[]} form={{name:'',kind:'history',source_id:''}} busy={false} preview={null} onChange={()=>{}} onFile={()=>{}} onPreview={()=>{}} onCommit={()=>{}}/>);
 expect(html).toContain('不会直接读取');expect(html).toContain('浏览器资料文件');expect(html).toContain('预览导入');expect(html).not.toContain('确认导入');
});
it('shows exact preview evidence and locks controls during publication',()=>{
 const html=renderToStaticMarkup(<BrowserImportForm items={[]} form={{name:'Neutral',kind:'history',source_id:'',filename:'history.json'}} busy preview={{records:2,matched:1,skipped:1,duplicates:3,samples:[{url:'https://example.test/neutral',title:'<img src=x>',visited_at:''}]}} onChange={()=>{}} onFile={()=>{}} onPreview={()=>{}} onCommit={()=>{}}/>);
 expect(html).toContain('确认导入');expect(html).toContain('1 条');expect(html).toContain('&lt;img');expect(html).toMatch(/<button[^>]*disabled[^>]*>确认导入/);
});
