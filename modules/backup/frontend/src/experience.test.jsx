import { afterEach, expect, it, vi } from 'vitest';
import { act, render, screen, within, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import App from './App.jsx';
import TaskEditor from './TaskEditor.jsx';

afterEach(() => vi.useRealTimers());
function clientFixture() {
  const data = {version: '0.0.2', state: {tasks: [], runs: [], retain: 100}};
  return {
    data, state: vi.fn(async () => structuredClone(data)),
    authorize: vi.fn(async kind => kind === 'source' ? '/data/example' : '/mount/example'),
    saveTask: vi.fn(async task => { const saved = {...task, id: 'new'}; data.state.tasks = [saved]; return saved; }),
    run: vi.fn(), setRetention: vi.fn(async () => ({})),
  };
}
async function fillTask(user) {
  await user.type(screen.getByLabelText('任务名称'), '我的备份');
  for (const button of screen.getAllByRole('button', {name:'选择目录', exact:true})) await user.click(button);
}

it('only exposes schedule fields when enabled and explains when writes will happen', async () => {
  const user = userEvent.setup();
  render(<TaskEditor client={clientFixture()} onClose={vi.fn()} onSaved={vi.fn()} />);
  expect(screen.queryByLabelText('执行频率')).not.toBeInTheDocument();
  expect(screen.getByText(/仅创建任务，不会开始备份/)).toBeVisible();
  await user.click(screen.getByLabelText('启用定时执行'));
  expect(screen.getByLabelText('执行频率')).toBeVisible();
  expect(screen.getByText(/保存后按计划自动备份/)).toBeVisible();
  await user.selectOptions(screen.getByLabelText('执行频率'), 'interval');
  await user.clear(screen.getByLabelText('间隔（分钟）'));
  await user.type(screen.getByLabelText('间隔（分钟）'), '45');
  await user.click(screen.getByLabelText('启用定时执行'));
  await user.click(screen.getByLabelText('启用定时执行'));
  expect(screen.getByLabelText('间隔（分钟）')).toHaveValue(45);
});

it('warns before discarding a changed form and keeps its contents when continuing', async () => {
  const user = userEvent.setup(), close = vi.fn();
  render(<TaskEditor client={clientFixture()} onClose={close} onSaved={vi.fn()} />);
  await user.type(screen.getByLabelText('任务名称'), '未保存');
  await user.click(screen.getByRole('button', {name:'取消', exact:true}));
  expect(close).not.toHaveBeenCalled();
  const warning = screen.getByRole('dialog', {name:'放弃未保存的修改？'});
  await user.click(within(warning).getByRole('button', {name:'继续编辑'}));
  expect(screen.getByLabelText('任务名称')).toHaveValue('未保存');
  await user.keyboard('{Escape}');
  await user.click(screen.getByRole('button', {name:'放弃修改'}));
  expect(close).toHaveBeenCalledOnce();
});

it('confirms a new task and offers a separate read-only preview without starting anything', async () => {
  const client = clientFixture(), user = userEvent.setup();
  render(<App client={client} />);
  await user.click((await screen.findAllByRole('button', {name:'新建任务', exact:true}))[0]);
  await fillTask(user);
  await user.click(screen.getByRole('button', {name:'创建任务', exact:true}));
  expect(await screen.findByRole('status')).toHaveTextContent('任务已创建');
  expect(screen.getByRole('status')).toHaveTextContent('定时执行未启用');
  expect(client.run).not.toHaveBeenCalled();
  await user.click(screen.getByRole('button', {name:'预览此任务'}));
  expect(client.run).toHaveBeenCalledWith('new', {kind:'preview'});
});

it('does not invite duplicate creation if saving succeeds but refreshing fails', async () => {
  const client = clientFixture(), user = userEvent.setup();
  render(<App client={client} />);
  await user.click((await screen.findAllByRole('button', {name:'新建任务', exact:true}))[0]);
  await fillTask(user);
  client.state.mockRejectedValue(new Error('连接暂时中断'));
  await user.click(screen.getByRole('button', {name:'创建任务', exact:true}));
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
  expect(screen.getByRole('status')).toHaveTextContent('任务已创建');
  expect(screen.getByRole('alert')).toHaveTextContent('连接暂时中断');
  expect(client.saveTask).toHaveBeenCalledOnce();
});

it('recovers polling errors but never hides an unsuccessful user action', async () => {
  const client = clientFixture();
  vi.useFakeTimers();
  client.state.mockRejectedValueOnce(new Error('连接暂时中断'));
  render(<App client={client} />);
  await act(async () => {});
  expect(screen.getByRole('alert')).toHaveTextContent('连接暂时中断');
  await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  vi.useRealTimers();
  const user = userEvent.setup();
  await user.click(screen.getByRole('button', {name:'设置与关于'}));
  client.setRetention.mockRejectedValue(new Error('设置未保存'));
  await user.click(screen.getByRole('button', {name:'保存设置'}));
  expect(screen.getByRole('alert')).toHaveTextContent('设置未保存');
  await user.click(screen.getByRole('button', {name:'备份任务'}));
  await user.click(screen.getByRole('button', {name:'刷新', exact:true}));
  expect(screen.getByRole('alert')).toHaveTextContent('设置未保存');
});

it('confirms settings saves separately from backup results', async () => {
  const user = userEvent.setup();
  render(<App client={clientFixture()} />);
  await user.click(await screen.findByRole('button', {name:'设置与关于'}));
  await user.click(screen.getByRole('button', {name:'保存设置'}));
  expect(await screen.findByRole('status')).toHaveTextContent('设置已保存');
});
