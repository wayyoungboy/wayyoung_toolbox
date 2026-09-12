import { it, expect, vi } from "vitest";
import { render, screen, within, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { FolderPicker, Modal, Status, bytes, date } from "./ui.jsx";

it("only selects authorized roots and navigates up without an escape path", async () => {
  const client = {
    directories: vi.fn(async (_, path) => ({
      path,
      parent: path === "/root/child" ? "/root" : "",
      roots: [{ name: "授权根", path: "/root" }],
      entries:
        path === "/root" ? [{ name: "子目录", path: "/root/child" }] : [],
      truncated: path === "/root/child",
    })),
  };
  const done = vi.fn(),
    user = userEvent.setup();
  render(
    <FolderPicker
      client={client}
      kind="source"
      onSelect={done}
      onClose={vi.fn()}
    />,
  );
  await user.click(await screen.findByRole("button", { name: /授权根/ }));
  await user.click(await screen.findByRole("button", { name: /子目录/ }));
  await expect(screen.findByText(/仅显示前 2000/)).resolves.toBeVisible();
  await user.click(screen.getByRole("button", { name: "上一级" }));
  await user.click(screen.getByRole("button", { name: "选择此目录" }));
  expect(done).toHaveBeenCalledWith("/root");
});

it("displays folder lookup errors and cannot select a failed listing", async () => {
  const client = {
    directories: vi.fn(async () => {
      throw Error("挂载失联");
    }),
  };
  render(
    <FolderPicker
      client={client}
      kind="target"
      onSelect={vi.fn()}
      onClose={vi.fn()}
    />,
  );
  expect(await screen.findByRole("alert")).toHaveTextContent("挂载失联");
  expect(screen.getByRole("button", { name: "选择此目录" })).toBeDisabled();
});

it("traps modal keyboard focus and closes on Escape", async () => {
  const close = vi.fn(),
    user = userEvent.setup();
  render(
    <Modal title="焦点测试" onClose={close}>
      <button>操作</button>
    </Modal>,
  );
  const modal = screen.getByRole("dialog");
  await user.tab();
  expect(
    within(modal).getByRole("button", { name: "关闭对话框" }),
  ).toHaveFocus();
  await user.tab({ shift: true });
  expect(within(modal).getByRole("button", { name: "操作" })).toHaveFocus();
  await user.keyboard("{Escape}");
  expect(close).toHaveBeenCalledOnce();
});

it("labels uncertain publication distinctly from confirmed completion", () => {
  render(<Status value="publication_unknown" />);
  expect(screen.getByText("提交待核对")).toBeVisible();
  expect(bytes(1024)).toBe("1.0 KiB");
  expect(bytes(1024 ** 3)).toBe("1.0 GiB");
  expect(date("0001-01-01")).toBe("—");
  expect(date(null)).toBe("—");
});

it("keeps editing focus across polling rerenders and honors current busy state", async () => {
  const close = vi.fn(),
    user = userEvent.setup();
  const { rerender } = render(
    <Modal title="编辑" onClose={() => close()}>
      <input aria-label="名称" />
    </Modal>,
  );
  const input = screen.getByRole("textbox", { name: "名称" });
  await user.click(input);
  rerender(
    <Modal title="编辑" busy onClose={() => close()}>
      <input aria-label="名称" />
    </Modal>,
  );
  expect(input).toHaveFocus();
  await user.keyboard("{Escape}");
  expect(close).not.toHaveBeenCalled();
  rerender(
    <Modal title="编辑" onClose={() => close()}>
      <input aria-label="名称" />
    </Modal>,
  );
  await user.keyboard("{Escape}");
  expect(close).toHaveBeenCalledOnce();
});
