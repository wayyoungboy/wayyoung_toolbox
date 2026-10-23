import { expect, it, vi } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
vi.mock('@douyinfe/semi-ui/lib/es/modal', () => ({ default: ({ children, title }) => <section aria-label={title}>{children}</section> }));
import CatalogPlayer from './CatalogPlayer';

it('plays only a registered asset and advertises registered subtitle tracks', () => {
  const html = renderToStaticMarkup(<CatalogPlayer title="Test title" asset={{ id: 'asset-1', path: 'TEST-001.mp4', progress: { revision: 0 }, subtitles: [{ index: 0, label: 'Chinese', language: 'zh' }] }} onClose={() => {}} />);
  expect(html).toContain('<video');
  expect(html).toContain('/api/catalog/assets/asset-1/stream');
  expect(html).toContain('/api/catalog/assets/asset-1/subtitles/0');
  expect(html).toContain('kind="subtitles"');
  expect(html).toContain('controls=""');
  expect(html).not.toContain('/vol4/');
});
