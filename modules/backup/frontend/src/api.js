const prefix = "/app/backup_center/api";
let sdkPromise;
async function nativeSDK() {
  sdkPromise ||= import("@trimjs/web-app")
    .then(async ({ TrimApp }) => {
      const sdk = new TrimApp({ debug: false });
      await sdk.ready();
      return sdk;
    })
    .catch((error) => {
      sdkPromise = undefined;
      throw error;
    });
  return sdkPromise;
}

// The desktop host starts its handshake when the iframe loads. Waiting until a
// picker click can miss that window. Keep one early connection; do not grant or
// open anything here. Failed initialization is reported by the picker on demand.
nativeSDK().catch(() => {});

export function createClient(fetcher = (...args) => fetch(...args)) {
  let csrf = "";
  async function request(path, method = "GET", body, signal) {
    const response = await fetcher(prefix + path, {
      method,
      signal,
      credentials: "same-origin",
      headers:
        method === "GET"
          ? {}
          : { "Content-Type": "application/json", "X-Backup-CSRF": csrf },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    });
    const data = await response.json().catch(() => null);
    if (!response.ok)
      throw new Error(data?.error || `请求失败（HTTP ${response.status}）`);
    if (data === null) throw new Error("服务响应无效，请刷新重试");
    return data;
  }
  const id = encodeURIComponent;
  return {
    async state(signal) {
      const data = await request("/state", "GET", undefined, signal);
      csrf = data.csrf;
      return data;
    },
    saveTask: (task) =>
      request(
        task.id ? `/tasks/${id(task.id)}` : "/tasks",
        task.id ? "PUT" : "POST",
        task,
      ),
    run: (taskID, options) =>
      request(`/tasks/${id(taskID)}/run`, "POST", options),
    cancel: (runID) => request(`/runs/${id(runID)}/cancel`, "POST", {}),
    removeTask: (taskID) => request(`/tasks/${id(taskID)}`, "DELETE", {}),
    setRetention: (retain) => request("/settings", "PUT", { retain }),
    snapshots: (taskID) => request(`/tasks/${id(taskID)}/snapshots`),
    directories: (kind, path = "") =>
      request(`/directories?${new URLSearchParams({ kind, path })}`),
    importRepository: (options) => request("/import", "POST", options),
    rebind: (taskID, target) =>
      request(`/tasks/${id(taskID)}/rebind`, "POST", { target }),
    async authorize(kind) {
      const sdk = await nativeSDK();
      // Standalone browser uses the in-app list of already authorized directories.
      // New OS permissions must be granted from the native fnOS desktop picker.
      if (sdk.isStandaloneWeb) return undefined;
      const result = await sdk.pickSharedFile({
        title: kind === "target" ? "选择备份目标目录" : "选择本地授权目录",
        okText: "选择此目录",
        sidebarGroup:
          kind === "target"
            ? ["remote"]
            : ["myFiles", "otherShare", "favorites"],
        creatable: false,
      });
      if (result && result.code !== 0)
        throw new Error(result.msg || "目录授权未完成");
      return result?.data?.[0] || null;
    },
  };
}
export const api = createClient();
