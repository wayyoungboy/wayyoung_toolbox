import { expect, it } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { CatalogAutomationForm } from './CatalogAutomation';

const props = { config: { after_organize: false, version: 1 }, enabled: false, onToggle() {}, onSave() {}, onReload() {} };
it('explains the opt-in future-only boundary instead of promising a full directory scan', () => {
  const html = renderToStaticMarkup(<CatalogAutomationForm {...props} />);
  expect(html).toContain('整理后自动登记馆藏');
  expect(html).toContain('仅影响之后成功整理的影片');
  expect(html).toContain('不会扫描已有目录');
  expect(html).toContain('已关闭');
  expect(html).toContain('已排队的登记任务需在处理队列单独取消');
  expect(html).not.toContain('checked=""');
});
it('locks mutations when loading, revision is unknown, a save is active or a write outcome is uncertain', () => {
  for (const state of [{ loading: true }, { config: null }, { saving: true }, { locked: true, error: '配置已变化' }]) {
    const html = renderToStaticMarkup(<CatalogAutomationForm {...props} {...state} />);
    expect(html).toMatch(/<button[^>]*disabled[^>]*>保存登记设置/);
    expect(html).toMatch(/<input[^>]*disabled/);
  }
  const html = renderToStaticMarkup(<CatalogAutomationForm {...props} error={'<script>bad</script>'} locked />);
  expect(html).toContain('&lt;script&gt;');
  expect(html).not.toContain('<script>');
  expect(html).toContain('重新读取登记设置');
});
