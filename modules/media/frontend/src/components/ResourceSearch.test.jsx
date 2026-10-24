import { expect, it, vi } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import ResourceSearch, { ResourceResults } from './ResourceSearch';

vi.mock('@douyinfe/semi-ui/lib/es/button', () => ({ default: ({ children, disabled, htmlType, onClick }) => <button type={htmlType || 'button'} disabled={disabled} onClick={onClick}>{children}</button> }));

const item = { infohash: 'a'.repeat(40), magnet: `magnet:?xt=urn:btih:${'a'.repeat(40)}`, name: '<script>Sample</script>', source: 'javdb', source_url: 'https://javdb.com/v/test', verification: 'unverified' };
const render = result => renderToStaticMarkup(<ResourceResults result={result} />);

it('makes network searches explicit and separate from cache reads or downloading', () => {
  const html = renderToStaticMarkup(<ResourceSearch initialCode="TEST-001" />);
  for (const text of ['完整番号', '读取上次结果', '搜索 JavDB', '不扫描影片', '不会自动下载', 'JAVSP']) expect(html).toContain(text);
  expect(html).not.toContain('立即下载');
});

it('renders a successful resource without pretending to verify size quality or content', () => {
  const html = render({ code: 'TEST-001', provider: 'javdb', state: 'ready', items: [item], searched_at: '2026-10-24T02:00:00Z' });
  for (const text of ['TEST-001', '找到 1 个候选', '未校验', '私有 tracker', '2026-10-24']) expect(html).toContain(text);
  expect(html).toContain('&lt;script&gt;Sample&lt;/script&gt;');
  expect(html).not.toContain('<script>');
  expect(html).toContain('rel="noopener noreferrer"');
  expect(html).toContain('referrerPolicy="no-referrer"');
  expect(html).toContain('<h3 role="status">');
});

it('distinguishes last successful cached resources from blocked or missing resources', () => {
  const stale = render({ code: 'TEST-001', state: 'blocked', cached: true, stale: true, items: [item] });
  expect(stale).toContain('站点拦截');
  expect(stale).toContain('上次成功结果');
  expect(stale).not.toContain('没有可用资源');
  expect(render({ code: 'TEST-001', state: 'empty', items: [] })).toContain('详情页未提供可用磁力');
  expect(render({ code: 'TEST-001', state: 'restricted', items: [] })).toContain('需要登录或权限');
  expect(render({ code: 'TEST-001', state: 'disabled', items: [] })).toContain('尚未为此类型启用');
  expect(render({ code: 'TEST-001', state: 'identity_mismatch', items: [] })).toContain('番号未确认一致');
});

it('does not turn unsafe source addresses into links', () => {
  for (const source_url of ['javascript:alert(1)', 'https://user:secret@javdb.com/v/test', '//internal.invalid/']) {
    const html = render({ code: 'TEST-001', state: 'ready', items: [{ ...item, source_url }] });
    expect(html).not.toContain('href=');
  }
});

it('offers explicit selection only when a caller owns a fresh actionable snapshot', () => {
  const result = { code: 'TEST-001', provider: 'javdb', state: 'ready', items: [item], searched_at: 'snapshot' };
  const html = renderToStaticMarkup(<ResourceResults result={result} onDownload={() => {}} downloadEnabled />);
  expect(html).toContain('选择下载');
  expect(html).not.toContain('disabled=""');
  for (const value of [{ ...result, stale: true }, { ...result, state: 'blocked' }]) {
    expect(renderToStaticMarkup(<ResourceResults result={value} onDownload={() => {}} downloadEnabled />)).toMatch(/disabled=""[^>]*>选择下载/);
  }
  expect(renderToStaticMarkup(<ResourceResults result={result} onDownload={() => {}} downloadEnabled={false} />)).toMatch(/disabled=""[^>]*>选择下载/);
});
