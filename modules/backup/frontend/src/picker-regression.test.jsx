import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import TaskEditor from "./TaskEditor.jsx";

const sdk = vi.hoisted(() => ({
  ready: vi.fn(async () => {}),
  isStandaloneWeb: true,
  pickSharedFile: vi.fn(),
}));
vi.mock("@trimjs/web-app", () => ({ TrimApp: class { constructor() { return sdk; } } }));

afterEach(() => {
  vi.restoreAllMocks();
  vi.useRealTimers();
  sdk.ready.mockReset().mockResolvedValue(undefined);
  sdk.pickSharedFile.mockReset();
  sdk.isStandaloneWeb = true;
});

describe("directory picker in a toolbox iframe", () => {
  it("can choose an already authorized directory without requesting another native grant", async () => {
    const client = {
      authorize: vi.fn(), saveTask: vi.fn(),
      directories: vi.fn(async (_kind, path = '') => ({ path, parent: '', roots: [{ name: '已有授权', path: '/data/allowed' }], entries: [] })),
    };
    const user = userEvent.setup();
    render(<TaskEditor client={client} onClose={vi.fn()} onSaved={vi.fn()} />);
    await user.click(screen.getByRole('button', { name: '从已授权目录选择源目录' }));
    await user.click(await screen.findByRole('button', { name: /已有授权/ }));
    await user.click(screen.getByRole('button', { name: '选择此目录' }));
    expect(screen.getByLabelText('源目录（只读）')).toHaveValue('/data/allowed');
    expect(client.authorize).not.toHaveBeenCalled();
    expect(client.saveTask).not.toHaveBeenCalled();
  });
  it("uses the toolbox host bridge without starting a second native handshake", async () => {
    const bridge = vi.fn(async () => "/authorized/docs");
    vi.spyOn(window, "parent", "get").mockReturnValue({
      location: { origin: window.location.origin, pathname: "/app/wayyoung_toolbox/" },
      wayyoungToolboxPickDirectory: bridge,
    });
    vi.resetModules();
    sdk.ready.mockClear();
    const { createClient } = await import("./api.js");
    expect(await createClient().authorize("source")).toBe("/authorized/docs");
    expect(bridge).toHaveBeenCalledWith(window, "source");
    expect(sdk.ready).not.toHaveBeenCalled();
  });

  it("falls back to authorized directories if an older toolbox has no bridge", async () => {
    vi.spyOn(window, "parent", "get").mockReturnValue({
      location: { origin: window.location.origin, pathname: "/app/wayyoung_toolbox/" },
    });
    vi.resetModules();
    sdk.ready.mockImplementation(() => new Promise(() => {}));
    const { createClient } = await import("./api.js");
    const resolved = vi.fn();
    createClient().authorize("source").then(resolved);
    await waitFor(() => expect(resolved).toHaveBeenCalledWith(undefined));
  });

  it("bounds an unanswered native handshake and falls back without opening a late native picker", async () => {
    vi.useFakeTimers();
    let finish;
    sdk.ready.mockImplementation(() => new Promise(resolve => { finish = resolve; }));
    sdk.isStandaloneWeb = false;
    vi.resetModules();
    const { createClient } = await import("./api.js");
    const resolved = vi.fn();
    createClient().authorize("source").then(resolved);
    await vi.advanceTimersByTimeAsync(6000);
    expect(resolved).toHaveBeenCalledWith(undefined);
    finish();
    await vi.advanceTimersByTimeAsync(0);
    expect(sdk.pickSharedFile).not.toHaveBeenCalled();
  });

  it("shows pending feedback, can cancel, and ignores a late native selection", async () => {
    let finish;
    const client = {
      authorize: vi.fn(() => new Promise(resolve => { finish = resolve; })),
      directories: vi.fn(),
      saveTask: vi.fn(),
    };
    const user = userEvent.setup();
    render(<TaskEditor client={client} onClose={vi.fn()} onSaved={vi.fn()} />);
    await user.click(screen.getAllByRole("button", { name: "选择目录" })[0]);
    expect(screen.getByRole("dialog", { name: "正在打开目录选择" })).toBeVisible();
    await user.click(screen.getByRole("button", { name: "取消目录选择" }));
    finish("/late/path");
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "正在打开目录选择" })).not.toBeInTheDocument());
    expect(screen.getByLabelText("源目录（只读）")).toHaveValue("");
    expect(client.saveTask).not.toHaveBeenCalled();
  });
});
