import { describe, it, expect, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import App from "./App.jsx";
import { StrictMode } from "react";

function fakeClient() {
  const task = {
    id: "task1",
    name: "文档测试",
    selection: {
      source: "/data/docs",
      target: "/mount/cloud",
      repo_id: "repo1",
    },
    schedule: {
      enabled: false,
      kind: "daily",
      at: "02:00",
      zone: "Asia/Shanghai",
    },
    retries: 1,
  };
  const data = {
    version: "0.0.1",
    state: { tasks: [task], runs: [], retain: 100 },
  };
  return {
    data,
    state: vi.fn(async () => data),
    saveTask: vi.fn(async (t) => {
      const saved = { ...t, id: t.id || "new-task" };
      data.state.tasks = [
        ...data.state.tasks.filter((x) => x.id !== saved.id),
        saved,
      ];
      return saved;
    }),
    run: vi.fn(async () => ({ id: "run1" })),
    cancel: vi.fn(async () => ({})),
    removeTask: vi.fn(async () => ({})),
    setRetention: vi.fn(async () => ({})),
    snapshots: vi.fn(async () => [
      {
        id: "snapshot1",
        created: "2026-09-03T00:00:00Z",
        files: [{ path: "a.txt", size: 3 }],
        digest: "hash",
      },
    ]),
    authorize: vi.fn(async (kind) =>
      kind === "target" ? "/mount/new" : "/data/new",
    ),
    directories: vi.fn(async () => ({
      roots: [{ name: "本地测试", path: "/data" }],
      entries: [],
      path: "",
      parent: "",
    })),
    importRepository: vi.fn(async () => ({})),
    rebind: vi.fn(async () => ({})),
  };
}

describe("backup application workflows", () => {
  it("uses final restored counts rather than the last in-flight progress", async () => {
    const client = fakeClient(),
      user = userEvent.setup();
    client.data.state.runs = [
      {
        id: "restore1",
        task_id: "task1",
        task_name: "文档测试",
        kind: "restore",
        status: "restored",
        progress: { files: 2, total: 3 },
        result: { copied: 3, unchanged: 0 },
      },
    ];
    render(<App client={client} />);
    expect(await screen.findByText("3 个文件 · 详见运行记录")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "运行记录" }));
    expect(screen.getByText("3 个文件")).toBeVisible();
  });

  it("shows scanned preview counts in task and run summaries without implying writes", async () => {
    const client = fakeClient(),
      user = userEvent.setup();
    client.data.state.runs = [
      {
        id: "preview1",
        task_id: "task1",
        task_name: "文档测试",
        kind: "preview",
        status: "previewed",
        progress: { files: 0 },
        result: { copied: 0 },
        preview: { files: 3, new: 1, changed: 1, unchanged: 1, removed: 1 },
      },
    ];
    render(<App client={client} />);
    expect(await screen.findByText("3 个文件 · 只读预览")).toBeVisible();
    expect(
      screen.getByText("新增 1 · 变化 1 · 未变化 1 · 源端删除 1（历史保留）"),
    ).toBeVisible();
    await user.click(screen.getByRole("button", { name: "运行记录" }));
    expect(screen.getByText("3 个文件")).toBeVisible();
  });

  it("includes reused files in completed backup counts when progress is absent", async () => {
    const client = fakeClient(),
      user = userEvent.setup();
    client.data.state.runs = [
      {
        id: "backup1",
        task_id: "task1",
        task_name: "文档测试",
        kind: "backup",
        status: "pending",
        result: { copied: 0, unchanged: 3, failed: 0 },
      },
    ];
    render(<App client={client} />);
    expect(await screen.findByText("3 个文件 · 详见运行记录")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "运行记录" }));
    expect(screen.getByText("3 个文件")).toBeVisible();
  });

  it("edits schedules, explicitly rebinds a mount and deletes configuration only", async () => {
    const client = fakeClient(),
      user = userEvent.setup();
    render(<App client={client} />);
    await user.click(
      await screen.findByRole("button", { name: "文档测试 更多操作" }),
    );
    await user.click(screen.getByRole("button", { name: "编辑任务" }));
    let modal = screen.getByRole("dialog");
    await user.click(within(modal).getByLabelText("启用定时执行"));
    await user.selectOptions(
      within(modal).getByLabelText("执行频率"),
      "weekly",
    );
    await user.selectOptions(within(modal).getByLabelText("星期"), "2");
    await user.selectOptions(
      within(modal).getByLabelText("执行频率"),
      "interval",
    );
    const interval = within(modal).getByLabelText("间隔（分钟）");
    await user.clear(interval);
    await user.type(interval, "30");
    await user.click(within(modal).getByText("重试与记录保留"));
    const retries = within(modal).getByLabelText("失败后重试次数");
    await user.clear(retries);
    await user.type(retries, "2");
    await user.click(within(modal).getByRole("button", { name: "保存任务" }));
    expect(client.saveTask.mock.lastCall[0].schedule).toMatchObject({
      enabled: true,
      kind: "interval",
      minutes: 30,
    });
    await user.click(screen.getByRole("button", { name: "文档测试 更多操作" }));
    await user.click(screen.getByRole("button", { name: "重新选择挂载" }));
    await user.click(
      await screen.findByRole("button", { name: "验证并重新绑定" }),
    );
    expect(client.rebind).toHaveBeenCalledWith("task1", "/mount/new");
    await user.click(screen.getByRole("button", { name: "文档测试 更多操作" }));
    await user.click(screen.getByRole("button", { name: "删除任务配置" }));
    modal = screen.getByRole("dialog");
    expect(modal).toHaveTextContent("源文件和云端历史备份均保留");
    await user.click(
      within(modal).getByRole("button", { name: "确认删除配置" }),
    );
    expect(client.removeTask).toHaveBeenCalledWith("task1");
  });

  it("imports an existing repository through a read-only recovery flow", async () => {
    const client = fakeClient(),
      user = userEvent.setup();
    render(<App client={client} />);
    await user.click(await screen.findByRole("button", { name: "恢复备份" }));
    await user.click(screen.getByRole("button", { name: "导入已有仓库" }));
    const modal = screen.getByRole("dialog");
    await user.click(within(modal).getByRole("button", { name: "选择目录" }));
    await user.type(within(modal).getByLabelText("仓库 ID"), "a".repeat(32));
    await user.click(within(modal).getByRole("button", { name: "验证并导入" }));
    expect(client.importRepository).toHaveBeenCalledWith({
      name: "恢复仓库",
      target: "/mount/new",
      repo_id: "a".repeat(32),
    });
  });

  it("shows recoverable request errors and rejects an unconfigured task", async () => {
    const client = fakeClient(),
      user = userEvent.setup();
    render(<App client={client} />);
    await user.click(await screen.findByRole("button", { name: "新建任务" }));
    let modal = screen.getByRole("dialog");
    await user.type(within(modal).getByLabelText("任务名称"), "缺少目录");
    await user.click(within(modal).getByRole("button", { name: "创建任务" }));
    expect(await within(modal).findByRole("alert")).toHaveTextContent(
      "请选择源目录与目标目录",
    );
    await user.click(within(modal).getByRole("button", { name: "取消" }));
    await user.click(screen.getByRole("button", { name: "放弃修改" }));
    client.run.mockRejectedValue(new Error("目标挂载丢失"));
    await user.click(screen.getByRole("button", { name: "立即备份" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("目标挂载丢失");
    await user.click(screen.getByRole("button", { name: "关闭错误提示" }));
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
  it("loads immediately after StrictMode aborts its first effect", async () => {
    render(
      <StrictMode>
        <App client={fakeClient()} />
      </StrictMode>,
    );
    expect(
      await screen.findByRole("button", { name: "文档测试", exact: true }),
    ).toBeVisible();
  });
  it("shows honest cloud status and separate preview / backup actions", async () => {
    const client = fakeClient();
    const user = userEvent.setup();
    render(<App client={client} />);
    expect(
      await screen.findByRole("heading", { name: "备份任务" }),
    ).toBeVisible();
    expect(screen.getByText(/文件写入挂载目录不等于云端已持久化/)).toBeVisible();
    await user.click(screen.getByRole("button", { name: "预览" }));
    expect(client.run).toHaveBeenCalledWith("task1", { kind: "preview" });
    await user.click(screen.getByRole("button", { name: "备份任务" }));
    await user.click(screen.getByRole("button", { name: "立即备份" }));
    expect(client.run).toHaveBeenCalledWith("task1", { kind: "backup" });
  });

  it("creates a task using native directory selection and scheduling off", async () => {
    const client = fakeClient();
    const user = userEvent.setup();
    render(<App client={client} />);
    await user.click(await screen.findByRole("button", { name: "新建任务" }));
    const modal = screen.getByRole("dialog");
    await user.type(within(modal).getByLabelText("任务名称"), "新备份");
    const pickers = within(modal).getAllByRole("button", { name: "选择目录" });
    await user.click(pickers[0]);
    await user.click(pickers[1]);
    await user.click(within(modal).getByRole("button", { name: "创建任务" }));
    await waitFor(() => expect(client.saveTask).toHaveBeenCalled());
    const saved = client.saveTask.mock.calls[0][0];
    expect(saved.selection.source).toBe("/data/new");
    expect(saved.selection.target).toBe("/mount/new");
    expect(saved.schedule.enabled).toBe(false);
  });

  it("allows scheduling to be enabled while creating a task", async () => {
    const client = fakeClient();
    const user = userEvent.setup();
    render(<App client={client} />);
    await user.click(await screen.findByRole("button", { name: "新建任务" }));
    const modal = screen.getByRole("dialog");
    await user.type(within(modal).getByLabelText("任务名称"), "定时备份");
    const schedule = within(modal).getByLabelText("启用定时执行");
    expect(schedule).toBeEnabled();
    await user.click(schedule);
    expect(schedule).toBeChecked();
    await user.selectOptions(
      within(modal).getByLabelText("执行频率"),
      "weekly",
    );
    const pickers = within(modal).getAllByRole("button", { name: "选择目录" });
    await user.click(pickers[0]);
    await user.click(pickers[1]);
    await user.click(within(modal).getByRole("button", { name: "创建任务" }));
    await waitFor(() => expect(client.saveTask).toHaveBeenCalled());
    expect(client.saveTask.mock.calls[0][0].schedule).toMatchObject({
      enabled: true,
      kind: "weekly",
    });
  });

  it("offers cancellation and bounded history configuration", async () => {
    const client = fakeClient();
    client.data.state.runs = [
      {
        id: "r1",
        task_id: "task1",
        task_name: "文档测试",
        kind: "backup",
        status: "running",
        started: "2026-09-03T00:00:00Z",
        progress: { files: 1, total: 3, phase: "copying" },
        result: {},
      },
    ];
    const user = userEvent.setup();
    render(<App client={client} />);
    await user.click(await screen.findByRole("button", { name: "运行记录" }));
    await user.click(screen.getByRole("button", { name: "取消任务" }));
    expect(client.cancel).toHaveBeenCalledWith("r1");
    await user.click(screen.getByRole("button", { name: "设置与关于" }));
    const retention = screen.getByLabelText("运行记录保留量");
    await user.clear(retention);
    await user.type(retention, "50");
    await user.click(screen.getByRole("button", { name: "保存设置" }));
    expect(client.setRetention).toHaveBeenCalledWith(50);
    expect(screen.getByText(/云端历史版本删除：关闭/)).toBeVisible();
  });

  it("restores a selected snapshot to a newly named local directory", async () => {
    const client = fakeClient();
    const user = userEvent.setup();
    render(<App client={client} />);
    await user.click(await screen.findByRole("button", { name: "恢复备份" }));
    await user.click(await screen.findByRole("button", { name: "恢复此版本" }));
    const modal = screen.getByRole("dialog");
    await user.click(within(modal).getByRole("button", { name: "选择目录" }));
    const name = within(modal).getByLabelText("新目录名称");
    await user.clear(name);
    await user.type(name, "恢复测试");
    await user.click(within(modal).getByRole("button", { name: "开始恢复" }));
    expect(client.run).toHaveBeenCalledWith("task1", {
      kind: "restore",
      snapshot_id: "snapshot1",
      destination: "/data/new/恢复测试",
    });
  });
});
