import { expect, it } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import CatalogMetadata from './CatalogMetadata';

it('exposes actor and tag filters as keyboard-accessible buttons', () => {
  const html = renderToStaticMarkup(<CatalogMetadata work={{ actors: ['Test Actor'], genres: ['Travel'], metadata_source: 'local_nfo' }} onSearch={() => {}} />);
  expect(html).toContain('aria-label="查找演员：Test Actor"');
  expect(html).toContain('aria-label="按标签筛选：Travel"');
  expect(html).toContain('<button');
  expect(html).not.toContain('href="#"');
});
