import { useEffect, useRef, useState } from 'react';
import Button from '@douyinfe/semi-ui/lib/es/button';
import { api } from '../api';
import { resourceSelectionFromResult } from '../resource-download-view';
import ResourceDownloads, { ResourceDownloadDialog } from './ResourceDownloads';
import '../resources.css';

const states = {
  not_searched: '尚未查询。读取缓存不会访问外站。',
  empty: '已读取详情页，但详情页未提供可用磁力。',
  not_found: '来源没有找到此番号，不代表其他站点也没有。',
  restricted: '来源需要登录或权限；未尝试绕过验证。',
  blocked: '查询遇到站点拦截；未绕过 Cloudflare 或证书验证。',
  identity_mismatch: '来源详情与查询番号未确认一致，已拒绝返回候选。',
  unavailable: '来源暂不可用或页面无法解析；不等于没有资源。',
  config_unavailable: '组件配置暂不可读或无效，请检查 JAVSP；上次结果仍保留。',
  disabled: 'JAVSP 尚未为此类型启用 JavDB，请到设置检查来源。',
  timed_out: '资源查询超时；不等于没有资源。',
};

function sourceLink(raw) {
  try {
    const url = new URL(raw);
    return ['https:', 'http:'].includes(url.protocol) && !url.username && !url.password ? url.href : '';
  } catch { return ''; }
}

export function ResourceResults({ result, onDownload, downloadEnabled = false }) {
  if (!result) return null;
  const items = result.items || [];
  return <section className="resource-results" aria-label="资源查询结果">
    <h3 role={result.state === 'ready' ? 'status' : undefined}>{result.code} · {result.state === 'ready' ? `找到 ${items.length} 个候选` : '查询状态'}</h3>
    {result.state !== 'ready' && <p role="status">{states[result.state] || '查询未成功，请核对后手动重试。'}</p>}
    {result.stale ? <p className="catalog-alert">以下为上次成功结果，不代表本次查询成功；有效性尚未重新确认。</p> : result.cached ? <p className="catalog-help">本地缓存，不会因读取而查询外站。</p> : null}
    {result.searched_at && <p className="catalog-help">上次成功：<time dateTime={result.searched_at}>{result.searched_at}</time></p>}
    {result.attempted_at && result.stale && <p className="catalog-help">本次尝试：<time dateTime={result.attempted_at}>{result.attempted_at}</time></p>}
    {items.length > 0 && <>
      <p className="catalog-help">候选的大小、做种、画质与实际内容均未校验。磁力仅保留 BTIH；私有 tracker 参数已移除，不适用于需要 passkey 的私有站。</p>
      <ul className="resource-candidates">{items.map(item => <ResourceCandidate key={item.infohash} item={item} onDownload={onDownload} downloadEnabled={downloadEnabled && result.state === 'ready' && !result.stale} />)}</ul>
    </>}
  </section>;
}

function ResourceCandidate({ item, onDownload, downloadEnabled }) {
  const [notice, setNotice] = useState('');
  const field = useRef(null);
  const href = sourceLink(item.source_url);
  const copy = async () => {
    setNotice('');
    if (!navigator.clipboard?.writeText) {
      field.current?.focus(); field.current?.select();
      setNotice('当前连接不支持剪贴板，已选中磁力，请手动复制。'); return;
    }
    try { await navigator.clipboard.writeText(item.magnet); setNotice('磁力已复制，未提交下载。'); }
    catch { setNotice('复制失败，请从下方文本框手动复制。'); }
  };
  return <li className="resource-candidate">
    <div className="resource-candidate-heading"><strong>{item.name || '未提供资源名称'}</strong><span>JavDB · 未校验</span></div>
    <p className="resource-hash">BTIH {item.infohash}</p>
    <label>磁力链接<textarea ref={field} readOnly value={item.magnet} rows={2} aria-label={`磁力链接 ${item.infohash}`} /></label>
    <div className="catalog-actions">{onDownload && <Button theme="solid" disabled={!downloadEnabled} onClick={() => onDownload(item.infohash)}>选择下载</Button>}<Button onClick={copy}>复制磁力</Button>{href && <a href={href} target="_blank" rel="noopener noreferrer" referrerPolicy="no-referrer">查看来源页</a>}</div>
    {notice && <p role="status" className="catalog-help">{notice}</p>}
  </li>;
}

export default function ResourceSearch({ initialCode = '', onNavigate }) {
  const [code, setCode] = useState(initialCode);
  const [result, setResult] = useState(null);
  const [busy, setBusy] = useState('');
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [accepted, setAccepted] = useState(false);
  const [selection, setSelection] = useState(null);
  const [downloadRevision, setDownloadRevision] = useState(0);
  const selectionLock = useRef(false);
  const active = useRef(null);
  const epoch = useRef(0);
  useEffect(() => () => { ++epoch.current; active.current?.abort(); }, []);
  const canonical = code.trim().toUpperCase();
  const valid = canonical.length <= 128 && /^[A-Z0-9]+(?:-[A-Z0-9]+)*$/.test(canonical);
  const perform = async mode => {
    if (!valid || busy || selectionLock.current) return;
    const current = ++epoch.current;
    const controller = new AbortController();
    active.current?.abort(); active.current = controller;
    setBusy(mode); setError(''); setNotice(''); setAccepted(false);
    try {
      const value = await (mode === 'cache' ? api.resourceCache(canonical, { signal: controller.signal }) : api.resourceSearch(canonical, { signal: controller.signal }));
      if (current === epoch.current) { setResult(value); setAccepted(true); }
    } catch (failure) {
      if (current === epoch.current && failure.name !== 'AbortError') setError(`${failure.message}；没有自动重试。`);
    } finally {
      if (current === epoch.current) { setBusy(''); active.current = null; }
    }
  };
  const cancel = () => {
    ++epoch.current; active.current?.abort(); active.current = null;
    setBusy(''); setAccepted(false); setNotice('已停止等待。可以重新读取缓存核对；没有提交下载。');
  };
  const chooseDownload = hash => {
    if (selectionLock.current || busy || error) return;
    const choice = resourceSelectionFromResult(result, hash, canonical, accepted);
    if (!choice) return;
    selectionLock.current = true; setSelection(choice);
  };
  const matching = result?.code === canonical;
  return <section className="resource-search" aria-label="按番号搜索资源">
    <p className="catalog-help">只使用 JAVSP 已配置并为该类型启用的 JavDB 与代理。不扫描影片、不运行刮削整理，也不会自动下载。</p>
    <form onSubmit={event => { event.preventDefault(); perform('search'); }}>
      <label>完整番号<input value={code} disabled={Boolean(busy || selection)} onChange={event => { setCode(event.target.value); setError(''); setNotice(''); setAccepted(false); }} placeholder="例如 TEST-001" maxLength={128} autoComplete="off" spellCheck="false" /></label>
      <div className="catalog-actions">
        <Button disabled={!valid || Boolean(busy || selection)} onClick={() => perform('cache')}>读取上次结果</Button>
        <Button theme="solid" htmlType="submit" disabled={!valid || Boolean(busy || selection)}>搜索 JavDB</Button>
        {onNavigate && <Button disabled={!valid || Boolean(busy || selection)} onClick={() => onNavigate('subscriptions', 'wants', null, { code: canonical })}>在想要清单登记</Button>}
        {busy && <Button onClick={cancel}>取消查询</Button>}
      </div>
    </form>
    {busy && <p role="status">{busy === 'cache' ? '正在读取本地缓存…' : '正在查询 JavDB，最长 45 秒…'}</p>}
    {error && <p className="catalog-alert" role="alert">{error}{matching && ' 下方为之前显示的结果，本次请求没有得到确认。'}</p>}
    {notice && <p role="status" className="catalog-help">{notice}</p>}
    {matching && <ResourceResults result={result} onDownload={chooseDownload} downloadEnabled={accepted && !busy && !error && !selection} />}
    <ResourceDownloads revision={downloadRevision} onNavigate={onNavigate} />
    {selection && <ResourceDownloadDialog selection={selection} onNavigate={onNavigate} onRegistered={() => setDownloadRevision(value => value + 1)} onClose={invalidate => { selectionLock.current = false; setSelection(null); if (invalidate) setAccepted(false); }} />}
  </section>;
}
