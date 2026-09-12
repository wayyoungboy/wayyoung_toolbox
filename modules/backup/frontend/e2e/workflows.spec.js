import { test, expect } from "@playwright/test";

async function fixture(page) {
  const task = {
    id: "a".repeat(32),
    name: "文档测试",
    selection: {
      source: "/data/example/documents",
      target: "/mount/example/备份测试",
      repo_id: "b".repeat(32),
    },
    schedule: {
      enabled: false,
      kind: "daily",
      at: "02:00",
      zone: "Asia/Shanghai",
    },
    retries: 1,
  };
  const state = {
    version: "0.0.1",
    csrf: "test-csrf",
    state: {
      tasks: [
        task,
        {
          ...task,
          id: "c".repeat(32),
          name: "照片测试",
          selection: {
            ...task.selection,
            source: "/data/example/photos",
            target: "/mount/example/照片备份",
          },
        },
      ],
      retain: 100,
      runs: [
        {
          id: "initial",
          task_id: task.id,
          task_name: task.name,
          kind: "backup",
          status: "pending",
          started: "2026-09-03T00:00:00Z",
          result: {
            copied: 2,
            unchanged: 1,
            failed: 0,
            snapshot_id: "d".repeat(32),
          },
        },
      ],
    },
  };
  const calls = [],
    errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("console", (message) => {
    if (message.type() === "error") errors.push(message.text());
  });
  await page.route("**/app/backup_center/api/**", async (route) => {
    const req = route.request(),
      url = new URL(req.url());
    const endpoint = url.pathname.split("/api")[1];
    const body = req.postDataJSON();
    calls.push({ endpoint, body });
    let result = {};
    if (endpoint === "/state") result = state;
    else if (endpoint.endsWith("/run")) {
      const run = {
        id: `new-run-${state.state.runs.length}`,
        task_id: task.id,
        task_name: task.name,
        kind: body.kind,
        status:
          body.kind === "preview"
            ? "previewed"
            : body.kind === "restore"
              ? "restored"
              : "pending",
        started: new Date().toISOString(),
        result: {
          copied: 1,
          unchanged: 2,
          failed: 0,
          notice: "挂载层校验通过，云端持久化待确认。",
        },
        ...(body.kind === "preview"
          ? { preview: { new: 1, changed: 1, unchanged: 1, removed: 0 } }
          : {}),
      };
      state.state.runs.unshift(run);
      result = run;
    } else if (endpoint.endsWith("/snapshots"))
      result = [
        {
          id: "d".repeat(32),
          created: "2026-09-03T00:00:00Z",
          files: [{ path: "readme.txt", size: 30 }],
        },
      ];
    else if (endpoint === "/directories") {
      const path = url.searchParams.get("path"),
        target = url.searchParams.get("kind") === "target";
      result = {
        path: path || "",
        parent: "",
        roots: [
          {
            name: target ? "测试挂载" : "已授权测试目录",
            path: target ? "/mount/example" : "/data/example",
          },
        ],
        entries: [],
      };
    } else if (endpoint === "/tasks" && req.method() === "POST") {
      result = { ...body, id: "new-task" };
      state.state.tasks.push(result);
    } else if (endpoint === "/settings") state.state.retain = body.retain;
    await route.fulfill({ status: 200, json: result });
  });
  await page.goto("/");
  await expect(page).toHaveTitle("飞牛备份中心");
  await expect(
    page.getByRole("heading", { name: "备份任务", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "文档测试", exact: true }),
  ).toBeVisible();
  await expect(page.locator("vite-error-overlay")).toHaveCount(0);
  return { state, calls, errors };
}

test("desktop: preview → backup → history → restore and native-style task editor", async ({
  page,
}, info) => {
  const { calls, errors } = await fixture(page);
  await page.screenshot({ path: info.outputPath("desktop-tasks.png") });
  await page.getByRole("button", { name: "新建任务", exact: true }).click();
  let modal = page.getByRole("dialog");
  await modal.getByLabel("任务名称", { exact: true }).fill("自动化测试");
  await page.screenshot({ path: info.outputPath("desktop-editor.png") });
  // Standalone SDK deliberately falls back to already authorized roots only.
  await modal
    .getByRole("button", { name: "选择目录", exact: true })
    .first()
    .click();
  await page
    .getByRole("dialog", { name: "选择已授权目录" })
    .getByRole("button", { name: /已授权测试目录/ })
    .click();
  await page.getByRole("button", { name: "选择此目录", exact: true }).click();
  modal = page.getByRole("dialog", { name: "新建备份任务" });
  await modal
    .getByRole("button", { name: "选择目录", exact: true })
    .nth(1)
    .click();
  await page
    .getByRole("dialog", { name: "选择已授权目录" })
    .getByRole("button", { name: /测试挂载/ })
    .click();
  await page.getByRole("button", { name: "选择此目录", exact: true }).click();
  await page.getByRole("button", { name: "创建任务", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "自动化测试", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "预览", exact: true }).first().click();
  await expect(
    page.getByRole("heading", { name: "运行记录", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "备份任务", exact: true }).click();
  await page
    .getByRole("button", { name: "立即备份", exact: true })
    .first()
    .click();
  await expect(
    page.getByRole("heading", { name: "运行记录", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "恢复备份", exact: true }).click();
  await page.getByRole("button", { name: "恢复此版本", exact: true }).click();
  await page
    .getByRole("dialog", { name: "恢复到新目录" })
    .getByRole("button", { name: "选择目录", exact: true })
    .click();
  await page
    .getByRole("dialog", { name: "选择已授权目录" })
    .getByRole("button", { name: /已授权测试目录/ })
    .click();
  await page.getByRole("button", { name: "选择此目录", exact: true }).click();
  await page.getByLabel("新目录名称").fill("恢复验证");
  await page.getByRole("button", { name: "开始恢复", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "运行记录", exact: true }),
  ).toBeVisible();
  expect(
    calls.filter((c) => c.endpoint.endsWith("/run")).map((c) => c.body.kind),
  ).toEqual(["preview", "backup", "restore"]);
  expect(calls.find((c) => c.endpoint === "/tasks").body.schedule.enabled).toBe(
    false,
  );
  expect(errors).toEqual([]);
});

test("mobile: navigation, settings and modal remain visible", async ({
  page,
}, info) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const { errors } = await fixture(page);
  await page.screenshot({ path: info.outputPath("mobile-tasks.png") });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
  await page.getByRole("button", { name: "设置与关于", exact: true }).click();
  await page.getByLabel("运行记录保留量").fill("50");
  await page.getByRole("button", { name: "保存设置", exact: true }).click();
  await page.getByRole("button", { name: "备份任务", exact: true }).click();
  await page.getByRole("button", { name: "新建任务", exact: true }).click();
  await expect(page.getByRole("dialog")).toBeVisible();
  const schedule = page.getByLabel("启用定时执行");
  await expect(schedule).toBeEnabled();
  await schedule.check();
  await expect(schedule).toBeChecked();
  await page.screenshot({ path: info.outputPath("mobile-editor.png") });
  await page.getByRole("button", { name: "关闭对话框", exact: true }).click();
  expect(errors).toEqual([]);
});
