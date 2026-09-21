import { expect, it } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { CompletedLibraryResults } from './CompletedLibrary';

it('shows identifiers, counts, timestamp and escaped filenames with no video-delete controls', () => {
  const html = renderToStaticMarkup(<CompletedLibraryResults report={{ scanned: true, root: '/completed', scanned_at: '2026-09-22T10:00:00Z', video_count: 3, id_count: 2, unrecognized_count: 1, unrecognized_examples: ['<unknown>.mp4'], total: 2, items: [{ media_id: 'TEST-001', file_count: 2, example_path: '<title>.mp4' }] }} />);
  for (const text of ['TEST-001', '最近识别', '无法识别', '&lt;unknown&gt;', '&lt;title&gt;']) expect(html).toContain(text);
  expect(html).not.toContain('<unknown>');
  expect(html).not.toContain('删除视频');
});

it('does not describe an unscanned directory as an empty library', () => {
  const html = renderToStaticMarkup(<CompletedLibraryResults report={{ scanned: false, items: [] }} />);
  expect(html).toContain('尚未识别');
  expect(html).not.toContain('已有 0');
});
