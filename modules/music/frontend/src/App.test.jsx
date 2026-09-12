import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import App from './App.jsx';

const settings = {
  source_dir: '/vol1/music/ncm',
  output_dir: '/vol1/music/output',
  recursive: true,
  preserve_tree: true,
  fetch_remote_art: false,
  migrate_mp3: false,
  conflict: 'skip',
  concurrency: 2,
  retain_runs: 100
};

function createClient(overrides = {}) {
  return {
    getHealth: vi.fn().mockResolvedValue({ app: 'music_converter', version: '0.0.4', status: 'ok' }),
    getSettings: vi.fn().mockResolvedValue(settings),
    saveSettings: vi.fn().mockResolvedValue(settings),
    scan: vi.fn().mockResolvedValue({
      total: 2,
      items: [
        { source: '/vol1/music/ncm/first.ncm', relative: 'first.ncm' },
        { source: '/vol1/music/ncm/album/second.ncm', relative: 'album/second.ncm' }
      ]
    }),
    startJob: vi.fn().mockResolvedValue({ job: { id: 'job-1', status: 'running', total: 2, completed: 0, failed: 0, skipped: 0 } }),
    getCurrentJob: vi.fn().mockRejectedValue(new Error('暂无转换任务')),
    cancelJob: vi.fn().mockResolvedValue({ canceling: true }),
    getRuns: vi.fn().mockResolvedValue({ runs: [] }),
    clearRuns: vi.fn().mockResolvedValue({ cleared: true }),
    getAbout: vi.fn().mockResolvedValue({
      project: { name: '音乐转换中心', version: '0.0.4' },
      dependencies: [{ name: 'ncmdump-go', role: 'actual_dependency', version: 'v1.7.5', license: 'MIT', repository: 'https://git.taurusxin.com/taurusxin/ncmdump-go' }],
      references: [{ name: 'ncmdump-gui', role: 'reference_only', license: 'MIT', repository: 'https://git.taurusxin.com/taurusxin/ncmdump-gui' }],
      usage: ['仅处理你合法拥有的本地文件', '仅供学习与个人研究使用', '用途声明不能替代上游许可证或适用法律']
    }),
    listDirectories: vi.fn().mockResolvedValue({ current: '', parent: '', entries: [] }),
    ...overrides
  };
}

describe('音乐转换中心', () => {
  it('clears the waiting count when the batch started from this scan completes', async () => {
    const user = userEvent.setup();
    const client = createClient({ startJob: vi.fn().mockResolvedValue({ job: { id: 'fast-job', status: 'completed', total: 2, completed: 1, failed: 0, skipped: 1 } }) });
    render(<App client={client} />);
    await screen.findByDisplayValue('/vol1/music/ncm');
    await user.click(screen.getByRole('button', { name: '扫描文件' }));
    expect(await screen.findByText('待转换 2')).toBeVisible();
    await user.click(screen.getByRole('button', { name: '开始转换' }));
    await waitFor(() => expect(client.startJob).toHaveBeenCalledTimes(1));
    expect(await screen.findByText('待转换 0')).toBeVisible();
  });
  it('shows existing MP3 as a direct migration rather than an NCM conversion', async () => {
    const user = userEvent.setup();
    const client = createClient({ scan: vi.fn().mockResolvedValue({ total: 1, items: [{ source: '/vol1/music/ncm/ready.mp3', relative: 'ready.mp3' }] }) });
    render(<App client={client} />);
    await screen.findByDisplayValue('/vol1/music/ncm');
    await user.click(screen.getByRole('button', { name: '扫描文件' }));
    expect(await screen.findByText('MP3 · 直接迁移')).toBeVisible();
    expect(screen.getByText('MP3', { selector: 'td' })).toBeVisible();
  });
  it('loads the native task surface with persisted settings and version', async () => {
    render(<App client={createClient()} />);
    expect(await screen.findByRole('heading', { name: '音乐转换中心' })).toBeVisible();
    expect(screen.getByDisplayValue('/vol1/music/ncm')).toBeVisible();
    expect(screen.getByDisplayValue('/vol1/music/output')).toBeVisible();
    expect(screen.getByText('v0.0.4')).toBeVisible();
    expect(screen.getByRole('button', { name: '扫描文件' })).toBeEnabled();
    expect(screen.getByRole('button', { name: '开始转换' })).toBeDisabled();
  });

  it('scans first and only enables conversion after files are found', async () => {
    const user = userEvent.setup();
    const client = createClient({
      getCurrentJob: vi.fn().mockResolvedValue({
        id: 'job-old', status: 'completed', total: 1, completed: 1, failed: 0, skipped: 0,
        results: [{ source: '/vol1/music/ncm/first.ncm', output: '/vol1/music/output/first.mp3', format: 'mp3', status: 'completed' }]
      })
    });
    render(<App client={client} />);
    await screen.findByDisplayValue('/vol1/music/ncm');

    await user.click(screen.getByRole('button', { name: '扫描文件' }));
    expect(await screen.findByText('first.ncm')).toBeVisible();
    expect(screen.getByText('album/second.ncm')).toBeVisible();
    expect(screen.getByText('MP3')).toBeVisible();
    expect(screen.getByText('待转换 2')).toBeVisible();
    expect(screen.getByRole('button', { name: '开始转换' })).toBeEnabled();
    expect(client.startJob).not.toHaveBeenCalled();

    await user.click(screen.getByRole('button', { name: '开始转换' }));
    await waitFor(() => expect(client.startJob).toHaveBeenCalledTimes(1));
    expect(await screen.findByText('任务正在运行')).toBeVisible();
  });

  it('shows actual dependencies separately from references and keeps the legal boundary honest', async () => {
    const user = userEvent.setup();
    render(<App client={createClient()} />);
    await screen.findByRole('heading', { name: '音乐转换中心' });

    await user.click(screen.getByRole('button', { name: '关于' }));
    expect(await screen.findByRole('heading', { name: '第三方项目与许可证' })).toBeVisible();
    expect(screen.getByText('实际依赖')).toBeVisible();
    expect(screen.getByText('仅作参考')).toBeVisible();
    expect(screen.getByRole('link', { name: 'ncmdump-go' })).toHaveAttribute('href', 'https://git.taurusxin.com/taurusxin/ncmdump-go');
    expect(screen.getByText('仅处理你合法拥有的本地文件')).toBeVisible();
    expect(screen.getByText('用途声明不能替代上游许可证或适用法律')).toBeVisible();
    expect(screen.queryByText(/UnblockNeteaseMusic/)).not.toBeInTheDocument();
  });

  it('loads and clears run history', async () => {
    const user = userEvent.setup();
    const client = createClient({
      getRuns: vi.fn().mockResolvedValue({ runs: [{ id: 'run-1', status: 'completed', started_at: '2026-09-01T00:00:00Z', total: 2, completed: 2, skipped: 0, failed: 0 }] })
    });
    render(<App client={client} />);
    await screen.findByRole('heading', { name: '音乐转换中心' });
    await user.click(screen.getByRole('button', { name: '运行记录' }));
    await waitFor(() => expect(client.getRuns).toHaveBeenCalledTimes(1));
    expect(screen.getAllByText('2', { selector: 'td' })).toHaveLength(2);
    await user.click(screen.getByRole('button', { name: /清空记录/ }));
    await waitFor(() => expect(client.clearRuns).toHaveBeenCalledTimes(1));
    expect(await screen.findByText('暂无运行记录')).toBeVisible();
  });

  it('edits advanced settings and saves them', async () => {
    const user = userEvent.setup();
    const client = createClient();
    render(<App client={client} />);
    await screen.findByDisplayValue('/vol1/music/ncm');
    await user.click(screen.getByRole('button', { name: '设置' }));
    const concurrency = screen.getByDisplayValue('2', { selector: 'input' });
    await user.clear(concurrency);
    await user.type(concurrency, '4');
    const retention = screen.getByDisplayValue('100', { selector: 'input' });
    await user.clear(retention);
    await user.type(retention, '50');
    await user.click(screen.getByRole('checkbox', { name: /缺失封面时联网补齐/ }));
    await user.click(screen.getByRole('checkbox', { name: /同时迁移已有 MP3/ }));
    await user.click(screen.getByRole('button', { name: '保存设置' }));
    await waitFor(() => expect(client.saveSettings).toHaveBeenCalledWith(expect.objectContaining({ concurrency: 4, retain_runs: 50, fetch_remote_art: true, migrate_mp3: true })));
  });

  it('cancels an active conversion task', async () => {
    const user = userEvent.setup();
    const client = createClient({
      getCurrentJob: vi.fn().mockResolvedValue({ id: 'job-2', status: 'running', total: 3, completed: 1, failed: 0, skipped: 0 })
    });
    render(<App client={client} />);
    expect(await screen.findByText('任务正在运行')).toBeVisible();
    await user.click(screen.getByRole('button', { name: /取消任务/ }));
    await waitFor(() => expect(client.cancelJob).toHaveBeenCalledTimes(1));
    expect(await screen.findByText('正在安全取消任务')).toBeVisible();
  });

  it('updates task policies and uses the native folder picker for both paths', async () => {
    const user = userEvent.setup();
    const client = createClient({
      listDirectories: vi.fn(async (path) => ({ current: path || '/vol1', parent: '', entries: [] }))
    });
    render(<App client={client} />);
    const source = await screen.findByDisplayValue('/vol1/music/ncm');
    await user.clear(source);
    await user.type(source, '/vol1/new-ncm');
    const checkboxes = screen.getAllByRole('checkbox');
    await user.click(checkboxes[0]);
    await user.click(checkboxes[1]);
    const selectors = screen.getAllByRole('combobox');
    await user.selectOptions(selectors[0], 'rename');
    await user.selectOptions(selectors[1], '4');

    await user.click(screen.getByRole('button', { name: '选择源目录浏览' }));
    expect(await screen.findByRole('dialog', { name: '选择源目录' })).toBeVisible();
    await user.click(screen.getByRole('button', { name: '选择当前文件夹' }));

    await user.click(screen.getByRole('button', { name: '选择输出目录浏览' }));
    expect(await screen.findByRole('dialog', { name: '选择输出目录' })).toBeVisible();
    await user.click(screen.getByRole('button', { name: '关闭' }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });
});
