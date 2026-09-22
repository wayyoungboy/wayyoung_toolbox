import { afterEach, expect, it } from 'vitest';
import fs from 'node:fs';

afterEach(() => { document.head.innerHTML = ''; document.body.innerHTML = ''; delete document.documentElement.dataset.toolboxModule; });
it('replaces the standalone full-height sidebar with a compact embedded navigation', () => {
  const style = document.createElement('style');
  style.textContent = fs.readFileSync('src/style.css', 'utf8') + '\n' + fs.readFileSync('../../../web/embedded.css', 'utf8');
  document.head.append(style);
  document.body.innerHTML = '<div class="app-shell"><aside class="sidebar"><nav></nav></aside><main></main></div>';
  expect(getComputedStyle(document.querySelector('.sidebar')).height).toBe('100vh');
  document.documentElement.dataset.toolboxModule = 'backup';
  expect(getComputedStyle(document.querySelector('.sidebar')).height).toBe('auto');
});
