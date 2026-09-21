import { useEffect, useRef, useState } from 'react';
import Button from '@douyinfe/semi-ui/lib/es/button';
import Input from '@douyinfe/semi-ui/lib/es/input';
import { api, formatDate } from '../api';

export function CompletedLibraryResults({ report }) {
  if (!report?.scanned) return <p className="muted-text">尚未识别。点击“识别已有番号”只读取文件名，不会刮削、下载或删除影片。</p>;
  return <>
    <p>已有 {report.id_count} 个番号 · {report.video_count} 个视频 · 无法识别 {report.unrecognized_count} 个</p>
    <p className="cell-secondary">最近识别：{formatDate(report.scanned_at)}（此列表为快照，订阅投递前会重新检查）</p>
    <div className="queue-table-scroll"><table className="queue-table">
      <thead><tr><th scope="col">已有番号</th><th scope="col">文件数</th><th scope="col">示例文件（相对路径）</th></tr></thead>
      <tbody>{report.items.map(item => <tr key={item.media_id}><td>{item.media_id}</td><td>{item.file_count}</td><td className="library-path">{item.example_path}</td></tr>)}</tbody>
    </table></div>
    {report.items.length === 0 ? <p className="muted-text">没有匹配的番号。</p> : null}
    {report.unrecognized_count > 0 ? <details className="library-unknown"><summary>无法识别的文件（最多展示 20 个，不会作为去重依据）</summary><ul>{report.unrecognized_examples?.map(path => <li key={path}><code>{path}</code></li>)}</ul></details> : null}
  </>;
}

export default function CompletedLibrary() {
  const [report, setReport] = useState(null);
  const [error, setError] = useState('');
  const [scanError, setScanError] = useState('');
  const [loading, setLoading] = useState(false);
  const [scanning, setScanning] = useState(false);
  const [expanded, setExpanded] = useState(false);
  const [draft, setDraft] = useState('');
  const [query, setQuery] = useState('');
  const [offset, setOffset] = useState(0);
  const [refresh, setRefresh] = useState(0);
  const scanController = useRef(null);
  useEffect(() => () => scanController.current?.abort(), []);
  useEffect(() => {
    if (scanning) return;
    const controller = new AbortController();
    setLoading(true);
    api.completedLibrary({ query, offset, signal: controller.signal }).then(value => {
      if (!controller.signal.aborted) { setReport(value); setError(''); }
    }).catch(err => {
      if (!controller.signal.aborted) { setReport(null); setError(err.message); }
    }).finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [query, offset, refresh, scanning]);

  const scan = async () => {
    if (scanController.current) return;
    const controller = new AbortController();
    scanController.current = controller;
    setScanning(true); setError(''); setScanError(''); setReport(null); setExpanded(true);
    try {
      const value = await api.scanCompletedLibrary({ signal: controller.signal });
      if (!controller.signal.aborted) {
        setReport(value); setDraft(''); setQuery(''); setOffset(0); setRefresh(n => n + 1);
      }
    } catch (err) {
      if (!controller.signal.aborted) setScanError(err.message);
    } finally {
      scanController.current = null;
      if (!controller.signal.aborted) setScanning(false);
    }
  };
  return <section className="completed-library" aria-label="整理完成目录番号索引" aria-busy={loading || scanning}>
    <div className="library-heading"><div><h3>已有影片 · 番号去重</h3><p className="muted-text">每次订阅会核对整理完成目录，已有番号不再投递到 qB。</p></div><div className="row-actions"><Button loading={scanning} disabled={loading && !scanning} onClick={scan}>识别已有番号</Button><Button onClick={() => setExpanded(value => !value)}>{expanded ? '收起列表' : '查看列表'}</Button></div></div>
    {error || scanError ? <p role="alert" className="error-box">{error || scanError}。请在“设置 → 目录与命名”检查目录；无法核对时会暂停新的投递。</p> : null}
    {report?.root ? <p className="library-path muted-text">整理完成目录：{report.root}</p> : null}
    {expanded ? <>
      <p className="cell-secondary">只认可非空视频；忽略空目录、NFO、符号链接和临时下载文件。同番号不同清晰度也会跳过；不会改变已经投递的任务。</p>
      <form className="toolbar" onSubmit={event => { event.preventDefault(); setQuery(draft.trim()); setOffset(0); }}>
        <Input aria-label="搜索已有番号" value={draft} onChange={setDraft} placeholder="搜索番号，例如 TEST-001" showClear />
        <Button htmlType="submit" disabled={loading || scanning}>搜索</Button>
      </form>
      {scanning ? <p role="status">正在只读识别目录中的视频文件…</p> : loading ? <p role="status">正在读取列表…</p> : <CompletedLibraryResults report={report} />}
      {report?.scanned ? <div className="library-pagination"><span>匹配 {report.total} 个 · 每页 50 个</span><Button disabled={loading || scanning || offset === 0} onClick={() => setOffset(n => Math.max(0, n - 50))}>上一页</Button><Button disabled={loading || scanning || offset + 50 >= report.total} onClick={() => setOffset(n => n + 50)}>下一页</Button></div> : null}
    </> : report?.scanned ? <p className="cell-secondary">已识别 {report.id_count} 个番号 · 最近识别 {formatDate(report.scanned_at)}</p> : null}
  </section>;
}
