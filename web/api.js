export const APP_BASE = '/app/wayyoung_toolbox';

export function createRequest(fetcher = (...args) => fetch(...args), timeoutMs = 15000) {
  return async (path, options = {}) => {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(new Error('请求超时，请刷新状态后重试；不要重复提交任务。')), options.timeoutMs || timeoutMs);
    try {
      const response = await fetcher(`${APP_BASE}${path}`, {
        ...options, signal: controller.signal, credentials: 'same-origin',
        headers: { 'Content-Type': 'application/json', ...(options.headers || {}) },
      });
      if (!response.headers.get('Content-Type')?.includes('application/json')) throw new Error('服务未返回 JSON，请检查飞牛登录状态后刷新。');
      const payload = await response.json();
      if (!response.ok) throw new Error(payload?.error || `请求失败（HTTP ${response.status}）`);
      if (!payload || typeof payload !== 'object' || Array.isArray(payload)) throw new Error('服务响应无效');
      return payload;
    } catch (error) {
      if (controller.signal.aborted) throw controller.signal.reason;
      throw error;
    } finally { clearTimeout(timer); }
  };
}
