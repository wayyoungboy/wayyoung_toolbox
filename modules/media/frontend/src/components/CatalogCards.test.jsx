import { expect, it } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import CatalogCards from './CatalogCards';

it('renders real library metadata, edition counts and escaped titles', () => {
  const html = renderToStaticMarkup(<CatalogCards items={[
    { id: 'one', code: 'TEST-001', title: '<Imported title>', actors: ['Test Actor'], year: 2026, asset_count: 2, available_count: 2, cover_asset_id: 'asset', metadata_source: 'local_nfo' },
    { id: 'two', code: '', title: '_646-C', actors: [], asset_count: 1, available_count: 0, cover_asset_id: '', metadata_source: 'filename' },
  ]} onOpen={() => {}} />);
  expect(html).toContain('&lt;Imported title&gt;');
  expect(html).not.toContain('<Imported title>');
  expect(html).toContain('2 个版本 / 分卷');
  expect(html).toContain('_646-C');
  expect(html).toContain('番号待确认');
  expect(html).toContain('文件暂不可用');
  expect(html).toContain('暂无封面');
  expect(html).not.toContain('src=""');
});
