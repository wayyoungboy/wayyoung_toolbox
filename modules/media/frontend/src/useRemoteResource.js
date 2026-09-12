import { useCallback, useEffect, useState } from 'react';

// A changed loader owns a new request scope. Aborted/late responses from a
// different filter, task or unmounted view can never replace current data.
export function useRemoteResource(loader, pollMs = 0) {
  const [revision, setRevision] = useState(0);
  const [state, setState] = useState({ owner: null, data: null, loading: true, error: '' });
  const refresh = useCallback(() => {
    setState((previous) => ({ ...previous, loading: true }));
    setRevision((value) => value + 1);
  }, []);
  useEffect(() => {
    let disposed = false;
    let timer;
    let controller;
    const load = async () => {
      controller = new AbortController();
      setState((previous) => ({ owner: loader, data: previous.owner === loader ? previous.data : null, loading: true, error: '' }));
      try {
        const data = await loader(controller.signal);
        if (!disposed) setState({ owner: loader, data, loading: false, error: '' });
      } catch (error) {
        if (!disposed && error.name !== 'AbortError') setState({ owner: loader, data: null, loading: false, error: error.message });
      } finally {
        if (!disposed && pollMs > 0) timer = window.setTimeout(poll, pollMs);
      }
    };
    const poll = () => {
      if (document.visibilityState === 'hidden') timer = window.setTimeout(poll, pollMs);
      else load();
    };
    load();
    return () => { disposed = true; controller?.abort(); window.clearTimeout(timer); };
  }, [loader, pollMs, revision]);
  return { ...(state.owner === loader ? state : { data: null, loading: true, error: '' }), refresh };
}
