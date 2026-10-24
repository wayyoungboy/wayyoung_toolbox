import { expect, it } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { CatalogScanStatus } from './CatalogScanControl';

const actions = { onStart() {}, onAction() {}, onReload() {} };
const running = { id: 12, status: 'running', version: 4, processed_files: 34, discovered_files: 90, entries: 150, current_path: '样本/TEST-001.mp4' };
const render = props => renderToStaticMarkup(<CatalogScanStatus {...actions} ready {...props} />);

it('shows durable progress without claiming a complete-scan percentage or canceling on close', () => {
  const html = render({ job: running });
  for (const text of ['后台扫描中', '34 / 90', '150', '样本/TEST-001.mp4', '暂停扫描', '取消扫描', '关闭窗口不会停止扫描', '完整成功后才更新馆藏']) expect(html).toContain(text);
  expect(html).not.toContain('%');
  expect(html).not.toContain('继续扫描');
});

it('exposes only actions valid for the current lifecycle and retains read-only failure help', () => {
  const paused = render({ job: { ...running, status: 'paused' } });
  expect(paused).toContain('继续扫描');
  expect(paused).not.toContain('暂停扫描');
  const failed = render({ job: { ...running, status: 'failed', error: '<script>changed</script>' } });
  expect(failed).toContain('从头重试');
  expect(failed).toContain('&lt;script&gt;');
  expect(failed).not.toContain('<script>');
  const success = render({ job: { ...running, status: 'success', result: { work_count: 18, asset_count: 20, issue_count: 2 } } });
  expect(success).toContain('18 部作品');
  expect(success).toContain('20 个文件');
  expect(success).toContain('重新扫描');
});

it('locks mutations until a successful status read and after an uncertain write', () => {
  for (const state of [{ ready: false }, { busy: true }, { locked: true }]) {
    const html = render({ job: running, ...state });
    expect(html).toMatch(/<button[^>]*disabled[^>]*>暂停扫描/);
    expect(html).toMatch(/<button[^>]*disabled[^>]*>取消扫描/);
  }
  expect(render({ job: null })).toContain('扫描馆藏');
  expect(render({ locked: true, error: '请核对' })).toContain('重新读取扫描状态');
});
