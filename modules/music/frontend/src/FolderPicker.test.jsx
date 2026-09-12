import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import FolderPicker from './FolderPicker.jsx';

describe('FolderPicker', () => {
  it('navigates authorized folders, refreshes, moves up, and selects the current folder', async () => {
    const user = userEvent.setup();
    const listDirectories = vi.fn(async (path) => {
      if (path === '/vol1/Music') return { current: '/vol1/Music', parent: '/vol1', entries: [] };
      return { current: '/vol1', parent: '', entries: [{ name: 'Music', path: '/vol1/Music' }] };
    });
    const onSelect = vi.fn();
    render(<FolderPicker client={{ listDirectories }} open title="选择源目录" initialPath="/vol1" onCancel={vi.fn()} onSelect={onSelect} />);

    expect(await screen.findByText('Music')).toBeVisible();
    await user.click(screen.getByText('Music'));
    expect(await screen.findByText('/vol1/Music')).toBeVisible();
    await user.click(screen.getByRole('button', { name: '刷新目录' }));
    await waitFor(() => expect(listDirectories).toHaveBeenCalledWith('/vol1/Music'));
    await user.click(screen.getByRole('button', { name: /上一级/ }));
    await waitFor(() => expect(listDirectories).toHaveBeenCalledWith('/vol1'));
    await user.click(screen.getByText('Music'));
    await screen.findByText('/vol1/Music');
    await user.click(screen.getByRole('button', { name: '选择当前文件夹' }));
    expect(onSelect).toHaveBeenCalledWith('/vol1/Music');
  });

  it('shows loading failures and supports closing the dialog', async () => {
    const user = userEvent.setup();
    const onCancel = vi.fn();
    render(<FolderPicker client={{ listDirectories: vi.fn().mockRejectedValue(new Error('没有目录权限')) }} open title="选择输出目录" initialPath="/vol1" onCancel={onCancel} onSelect={vi.fn()} />);
    expect(await screen.findByText('没有目录权限')).toBeVisible();
    await user.click(screen.getByRole('button', { name: '关闭' }));
    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  it('renders nothing while closed', () => {
    const { container } = render(<FolderPicker client={{ listDirectories: vi.fn() }} open={false} title="选择目录" onCancel={vi.fn()} onSelect={vi.fn()} />);
    expect(container).toBeEmptyDOMElement();
  });
});
