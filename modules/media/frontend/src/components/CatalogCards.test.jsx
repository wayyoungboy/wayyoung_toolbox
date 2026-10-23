import { expect, it } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import CatalogCards from './CatalogCards';

it('uses a distinct accessible selection control without nesting buttons', () => {
 const html=renderToStaticMarkup(<CatalogCards items={[{id:'one',title:'Sample',actors:[],asset_count:1,available_count:1}]} selecting selectedIDs={['one']} onToggle={()=>{}} onOpen={()=>{}}/>);
 expect(html).toContain('type="checkbox"');
 expect(html).toContain('选择作品：Sample');
 expect(html).toContain('checked');
 expect(html).not.toMatch(/<button[^>]*>.*<input/s);
});
