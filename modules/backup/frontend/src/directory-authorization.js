const kinds = new Set(["source", "target", "restore"]);

// Keep the native connection in the direct fnOS child, never in a nested module.
// A missing host must not leave the directory button waiting forever. Timing out
// initialization only is safe: picking a directory itself may take the user time.
export function createDirectoryAuthorizer(loadSDK, timeoutMs = 5000) {
  let timer;
  const initialization = Promise.resolve().then(loadSDK).then(async sdk => {
    await sdk.ready();
    return sdk;
  });
  const ready = Promise.race([
    initialization,
    new Promise(resolve => { timer = setTimeout(() => resolve(undefined), timeoutMs); }),
  ]).catch(() => undefined).finally(() => clearTimeout(timer));

  return async kind => {
    if (!kinds.has(kind)) throw new Error("无效的目录选择类型");
    const sdk = await ready;
    // Only the server's existing authorized roots are offered by the fallback.
    if (!sdk || sdk.isStandaloneWeb) return undefined;
    const result = await sdk.pickSharedFile({
      title: kind === "target" ? "选择备份目标目录" : "选择本地授权目录",
      okText: "选择此目录",
      sidebarGroup: kind === "target" ? ["remote"] : ["myFiles", "otherShare", "favorites"],
      creatable: false,
    });
    if (result && result.code !== 0) throw new Error(result.msg && result.msg !== 'Operation failed' ? result.msg : '目录授权未完成，请重新选择；如果已授权，可使用已授权目录。');
    return result?.data?.[0] || null;
  };
}

export function toolboxParent(child = window) {
  try {
    const parent = child.parent;
    if (parent !== child && parent.location.origin === child.location.origin &&
        /^\/app\/wayyoung_toolbox\/?$/.test(parent.location.pathname)) return parent;
  } catch {
    // The native desktop may be cross-origin; use its SDK handshake instead.
  }
  return null;
}

export function installToolboxDirectoryBridge(host, authorize) {
  host.wayyoungToolboxPickDirectory = async (requester, kind) => {
    const activeFrame = () => host.document.querySelector('#module-content iframe[data-active="true"]');
    const frame = activeFrame();
    // No generic native RPC relay. Only this active, same-origin backup frame
    // can request the fixed shared-directory picker capability.
    if (!frame || frame.hidden || !frame.isConnected || frame.contentWindow !== requester ||
        requester.location.origin !== host.location.origin ||
        requester.location.pathname !== "/app/wayyoung_toolbox/modules/backup/" ||
        !kinds.has(kind)) throw new Error("目录选择请求来源无效");
    const generation = frame.dataset.generation;
    const activation = frame.dataset.activation;
    const result = await authorize(kind);
    if (activeFrame() !== frame || frame.hidden || !frame.isConnected || frame.dataset.generation !== generation || frame.dataset.activation !== activation) return null;
    return result;
  };
}
