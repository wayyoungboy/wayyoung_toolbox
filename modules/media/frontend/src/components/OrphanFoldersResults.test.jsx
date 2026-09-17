import { describe, expect, it, vi, afterEach } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { api } from '../api';
import OrphanFoldersResults from './OrphanFoldersResults';

// Semi UI imports browser-only CSS through CJS; exercise our column renderers
// with a lightweight table host in the server-rendered unit tests.
vi.mock('@douyinfe/semi-ui/lib/es/table', () => ({
  default: ({ dataSource, columns, empty }) => dataSource.length
    ? <div>{dataSource.map((row) => <div key={row.path}>{columns.map((column) => <div key={column.title}>{column.render ? column.render(row[column.dataIndex], row) : row[column.dataIndex]}</div>)}</div>)}</div>
    : <div>{empty}</div>,
}));

afterEach(() => vi.unstubAllGlobals());

describe('orphan folder maintenance', () => {
  it('separates preview from permanent cleanup', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, json: async () => ({}) });
    vi.stubGlobal('fetch', fetchMock);
    await api.previewOrphanFolders();
    await api.cleanupOrphanFolders();
    expect(fetchMock).toHaveBeenNthCalledWith(1, '/api/maintenance/orphan-folders/preview', expect.objectContaining({ method: 'POST' }));
    expect(fetchMock).toHaveBeenNthCalledWith(2, '/api/maintenance/orphan-folders/cleanup', expect.objectContaining({ method: 'POST' }));
  });
  it('shows eligible folders, skipped folders, file counts and deleted outcome without HTML injection', () => {
    const html = renderToStaticMarkup(<OrphanFoldersResults result={{ found: 1, removed: 1, deleted_files: 2, directories: ['/library/ABC-123'], candidates: [
      { path: '/library/ABC-123', files: ['movie.nfo', 'poster.jpg'], bytes: 2048 },
      { path: '/library/<img>', files: ['keep.txt'], bytes: 10, skip_reason: '包含非影片附件' },
    ] }} />);
    expect(html).toContain('已清理');
    expect(html).toContain('包含非影片附件');
    expect(html).toContain('movie.nfo');
    expect(html).toContain('poster.jpg');
    expect(html).toContain('&lt;img&gt;');
    expect(html).not.toContain('<img>');
  });
  it('shows a useful empty result', () => {
    expect(renderToStaticMarkup(<OrphanFoldersResults result={{ found: 0, candidates: [], directories: [] }} />)).toContain('未发现无视频残留目录');
  });
});
