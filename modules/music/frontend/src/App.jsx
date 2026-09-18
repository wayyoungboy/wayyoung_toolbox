import { useEffect, useMemo, useState } from 'react';
import {
  IconCheckCircleStroked,
  IconClock,
  IconDelete,
  IconFile,
  IconFolder,
  IconHelpCircle,
  IconHistory,
  IconMusic,
  IconRefresh,
  IconSearch,
  IconSetting,
  IconStop,
  IconTickCircle
} from '@douyinfe/semi-icons';
import { api } from './api.js';
import FolderPicker from './FolderPicker.jsx';

const emptySettings = {
  source_dir: '',
  output_dir: '',
  recursive: true,
  preserve_tree: true,
  fetch_remote_art: false,
  migrate_mp3: false,
  conflict: 'skip',
  concurrency: 2,
  retain_runs: 100
};

const navigation = [
  { id: 'tasks', label: '转换任务', icon: IconMusic },
  { id: 'runs', label: '运行记录', icon: IconHistory },
  { id: 'settings', label: '设置', icon: IconSetting },
  { id: 'about', label: '关于', icon: IconHelpCircle }
];

export default function App({ client = api }) {
  const [view, setView] = useState('tasks');
  const [health, setHealth] = useState({ version: '0.0.4', status: 'loading' });
  const [settings, setSettings] = useState(emptySettings);
  const [scanItems, setScanItems] = useState([]);
  const [job, setJob] = useState(null);
  const [activeBatchID, setActiveBatchID] = useState(null);
  const [runs, setRuns] = useState([]);
  const [about, setAbout] = useState(null);
  const [busy, setBusy] = useState('');
  const [notice, setNotice] = useState('');
  const [error, setError] = useState('');
  const [picker, setPicker] = useState(null);
  const [settingsReady, setSettingsReady] = useState(false);
  const [loadError, setLoadError] = useState('');
  const [reload, setReload] = useState(0);

  useEffect(() => {
    let active = true;
    setSettingsReady(false);
    setLoadError('');
    Promise.allSettled([client.getHealth(), client.getSettings(), client.getCurrentJob()]).then(([healthResult, settingsResult, jobResult]) => {
      if (!active) return;
      if (healthResult.status === 'fulfilled') setHealth(healthResult.value);
      else setHealth(current => ({ ...current, status: 'error' }));
      if (settingsResult.status === 'fulfilled') {
        setSettings(settingsResult.value);
        setSettingsReady(true);
      } else setLoadError(settingsResult.reason?.message || '配置读取失败');
      if (jobResult.status === 'fulfilled') setJob(jobResult.value);
    });
    return () => { active = false; };
  }, [client, reload]);

  useEffect(() => {
    if (!job || job.status !== 'running') return undefined;
    const timer = window.setInterval(async () => {
      try {
        const current = await client.getCurrentJob();
        setJob(current);
      } catch {
        // Keep the last visible state; the next refresh may succeed.
      }
    }, 1500);
    return () => window.clearInterval(timer);
  }, [client, job?.status]);

  const updateSetting = (key, value) => {
    setSettings((current) => ({ ...current, [key]: value }));
    setScanItems([]);
  };

  const save = async (quiet = false) => {
    if (!settingsReady) throw new Error('配置尚未读取，不能覆盖原有设置。请重新读取配置。');
    const saved = await client.saveSettings(settings);
    setSettings(saved);
    if (!quiet) setNotice('设置已保存');
    return saved;
  };

  const scan = async () => {
    setBusy('scan');
    setError('');
    setNotice('');
    try {
      await save(true);
      const response = await client.scan();
      setActiveBatchID(null);
      setScanItems(response.items || []);
      setNotice(response.total ? `已找到 ${response.total} 个待处理文件` : '没有找到待处理文件');
    } catch (reason) {
      setError(reason.message);
    } finally {
      setBusy('');
    }
  };

  const start = async () => {
    setBusy('start');
    setError('');
    try {
      const response = await client.startJob();
      setActiveBatchID(response.job.id);
      setJob(response.job);
      setNotice('转换任务已开始');
    } catch (reason) {
      setError(reason.message);
    } finally {
      setBusy('');
    }
  };

  const cancel = async () => {
    setBusy('cancel');
    setError('');
    try {
      await client.cancelJob();
      setNotice('正在安全取消任务');
    } catch (reason) {
      setError(reason.message);
    } finally {
      setBusy('');
    }
  };

  const switchView = async (next) => {
    setView(next);
    setError('');
    setNotice('');
    if (next === 'runs') {
      try {
        const response = await client.getRuns();
        setRuns(response.runs || []);
      } catch (reason) {
        setError(reason.message);
      }
    }
    if (next === 'about' && !about) {
      try {
        setAbout(await client.getAbout());
      } catch (reason) {
        setError(reason.message);
      }
    }
  };

  const totals = useMemo(() => ({
    waiting: job && (job.status === 'running' || job.id === activeBatchID) ? Math.max(0, (job.total || 0) - (job.completed || 0) - (job.failed || 0) - (job.skipped || 0) - (job.canceled || 0)) : scanItems.length,
    completed: job?.completed || 0,
    failed: job?.failed || 0,
    skipped: job?.skipped || 0
  }), [job, scanItems.length, activeBatchID]);

  return (
    <div className="app-shell">
      <header className="app-header">
        <div className="brand">
          <div className="brand-icon"><IconMusic /></div>
          <div>
            <h1>音乐转换中心</h1>
            <p>本地 NCM 还原为 MP3 / FLAC / M4A</p>
          </div>
        </div>
        <div className="service-meta">
          <span className={`service-status ${health.status === 'ok' ? 'healthy' : ''}`}><i />{health.status === 'ok' ? '服务正常' : '正在连接'}</span>
          <span className="version">v{health.version || '0.0.4'}</span>
          <button className="icon-button" type="button" aria-label="刷新状态" onClick={() => window.location.reload()}><IconRefresh /></button>
        </div>
      </header>

      <div className="workspace">
        <aside className="sidebar" aria-label="应用导航">
          {navigation.map(({ id, label, icon: Icon }) => (
            <button key={id} type="button" aria-label={label} className={view === id ? 'nav-item active' : 'nav-item'} onClick={() => switchView(id)}>
              <Icon /><span>{label}</span>
            </button>
          ))}
        </aside>

        <main className="content">
          {loadError && <div className="notice error" role="alert">{loadError}。未读取配置前不会保存或扫描。 <button className="secondary-button" onClick={() => setReload(value => value + 1)}>重新读取配置</button></div>}
          {(notice || error) && <div className={error ? 'notice error' : 'notice success'}>{error || notice}</div>}
          {view === 'tasks' && (
            <TasksView
              settings={settings}
              updateSetting={updateSetting}
              scanItems={scanItems}
              totals={totals}
              job={job}
              busy={!settingsReady ? 'loading' : busy}
              onScan={scan}
              onStart={start}
              onCancel={cancel}
              onPick={(kind) => setPicker(kind)}
            />
          )}
          {view === 'runs' && <RunsView runs={runs} onClear={async () => { await client.clearRuns(); setRuns([]); }} />}
          {view === 'settings' && <SettingsView settings={settings} updateSetting={updateSetting} onSave={() => save(false)} onPick={(kind) => setPicker(kind)} busy={!settingsReady ? 'loading' : busy} />}
          {view === 'about' && <AboutView about={about} />}
        </main>
      </div>

      <FolderPicker
        client={client}
        open={Boolean(picker)}
        title={picker === 'source_dir' ? '选择源目录' : '选择输出目录'}
        initialPath={picker ? settings[picker] : ''}
        onCancel={() => setPicker(null)}
        onSelect={(path) => { updateSetting(picker, path); setPicker(null); }}
      />
    </div>
  );
}

function DirectoryField({ label, value, onChange, onPick }) {
  return (
    <label className="directory-field">
      <span>{label}</span>
      <input value={value} onChange={(event) => onChange(event.target.value)} placeholder="请选择飞牛存储目录" />
      <button className="secondary-button browse-button" type="button" aria-label={`${label}浏览`} onClick={onPick}><IconFolder /> 浏览</button>
    </label>
  );
}

function TasksView({ settings, updateSetting, scanItems, totals, job, busy, onScan, onStart, onCancel, onPick }) {
  const running = job?.status === 'running';
  return (
    <section className="task-view">
      <div className="task-controls">
        <DirectoryField label="选择源目录" value={settings.source_dir} onChange={(value) => updateSetting('source_dir', value)} onPick={() => onPick('source_dir')} />
        <DirectoryField label="选择输出目录" value={settings.output_dir} onChange={(value) => updateSetting('output_dir', value)} onPick={() => onPick('output_dir')} />
        <div className="option-row">
          <label className="check-control"><input type="checkbox" checked={settings.recursive} onChange={(event) => updateSetting('recursive', event.target.checked)} />递归扫描</label>
          <label className="check-control"><input type="checkbox" checked={settings.preserve_tree} onChange={(event) => updateSetting('preserve_tree', event.target.checked)} />保留目录结构</label>
          <label>冲突处理策略
            <select value={settings.conflict} onChange={(event) => updateSetting('conflict', event.target.value)}>
              <option value="skip">跳过已存在文件</option>
              <option value="rename">自动重命名</option>
              <option value="overwrite">覆盖目标文件</option>
            </select>
          </label>
          <label>并发任务数
            <select value={settings.concurrency} onChange={(event) => updateSetting('concurrency', Number(event.target.value))}>
              {[1, 2, 3, 4, 5, 6, 7, 8].map((value) => <option key={value} value={value}>{value}</option>)}
            </select>
          </label>
        </div>
        <div className="primary-actions">
          <button className="primary-button" type="button" aria-label="扫描文件" disabled={Boolean(busy) || running} onClick={onScan}><IconSearch />{busy === 'scan' ? '正在扫描' : '扫描文件'}</button>
          <button className="primary-button" type="button" aria-label="开始转换" disabled={scanItems.length === 0 || Boolean(busy) || running} onClick={onStart}><IconTickCircle />开始转换</button>
          {running && <button className="danger-button" type="button" disabled={busy === 'cancel'} onClick={onCancel}><IconStop />取消任务</button>}
        </div>
      </div>

      {running && <div className="running-banner"><IconRefresh className="spin" /><strong>任务正在运行</strong><span>已完成 {job.completed || 0} / {job.total || 0}</span></div>}

      <div className="summary-rail" aria-label="转换摘要">
        <span><IconClock className="blue" />待转换 {totals.waiting}</span>
        <span><IconCheckCircleStroked className="green" />已完成 {totals.completed}</span>
        <span><i className="status-dot red" />失败 {totals.failed}</span>
        <span><i className="status-dot amber" />跳过 {totals.skipped}</span>
      </div>

      <div className="table-wrap">
        <table>
          <thead><tr><th>文件名</th><th>原格式</th><th>目标格式</th><th>状态</th><th>进度</th></tr></thead>
          <tbody>
            {scanItems.length === 0 && <tr><td colSpan="5"><div className="empty-state"><IconFile />选择目录后先扫描文件</div></td></tr>}
            {scanItems.map((item) => {
              const result = job?.results?.find((entry) => entry.source === item.source);
              const status = result?.status || (running ? 'waiting' : 'waiting');
              return (
                <tr key={item.source}>
                  <td><span className="file-name"><IconFile />{item.relative}</span></td>
                  <td>{item.relative.split('.').at(-1).toUpperCase()}</td>
                  <td>{result?.format?.toUpperCase() || (/\.mp3$/i.test(item.relative) ? 'MP3 · 直接迁移' : '自动识别')}</td>
                  <td><StatusLabel status={status} /></td>
                  <td><Progress status={status} /></td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </section>
  );
}

function StatusLabel({ status }) {
  const labels = { waiting: '等待中', completed: '已完成', failed: '失败', skipped: '已跳过', canceled: '已取消' };
  return <span className={`status-label ${status}`}><i />{labels[status] || status}</span>;
}

function Progress({ status }) {
  const value = status === 'completed' ? 100 : status === 'waiting' ? 0 : status === 'skipped' ? 0 : 35;
  return <div className="progress-cell"><div className="progress-track"><i style={{ width: `${value}%` }} /></div><span>{status === 'skipped' ? '—' : `${value}%`}</span></div>;
}

function RunsView({ runs, onClear }) {
  return (
    <section className="page-section">
      <header className="section-heading"><div><h2>运行记录</h2><p>保留最近的转换批次，不记录账号、Cookie 或在线服务凭据。</p></div><button className="danger-ghost" type="button" disabled={!runs.length} onClick={onClear}><IconDelete />清空记录</button></header>
      <div className="table-wrap">
        <table>
          <thead><tr><th>开始时间</th><th>状态</th><th>文件数</th><th>完成</th><th>跳过</th><th>失败</th></tr></thead>
          <tbody>
            {!runs.length && <tr><td colSpan="6"><div className="empty-state"><IconHistory />暂无运行记录</div></td></tr>}
            {runs.map((run) => <tr key={run.id}><td>{new Date(run.started_at).toLocaleString()}</td><td><StatusLabel status={run.status === 'completed' ? 'completed' : 'failed'} /></td><td>{run.total}</td><td>{run.completed}</td><td>{run.skipped}</td><td>{run.failed}</td></tr>)}
          </tbody>
        </table>
      </div>
    </section>
  );
}

function SettingsView({ settings, updateSetting, onSave, onPick }) {
  return (
    <section className="page-section settings-page">
      <header className="section-heading"><div><h2>设置</h2><p>源 NCM 不会自动删除；开启 MP3 迁移后，仅在目标保存成功时移除源 MP3。</p></div></header>
      <div className="settings-form">
        <DirectoryField label="源目录" value={settings.source_dir} onChange={(value) => updateSetting('source_dir', value)} onPick={() => onPick('source_dir')} />
        <DirectoryField label="输出目录" value={settings.output_dir} onChange={(value) => updateSetting('output_dir', value)} onPick={() => onPick('output_dir')} />
        <div className="settings-grid">
          <label><span>并发任务数</span><input type="number" min="1" max="8" value={settings.concurrency} onChange={(event) => updateSetting('concurrency', Number(event.target.value))} /></label>
          <label><span>运行记录保留量</span><input type="number" min="1" max="1000" value={settings.retain_runs} onChange={(event) => updateSetting('retain_runs', Number(event.target.value))} /></label>
        </div>
        <label className="switch-row"><span><strong>缺失封面时联网补齐</strong><small>关闭时完全使用 NCM 内嵌元数据；开启后仅访问文件元数据中提供的封面地址。</small></span><input type="checkbox" checked={settings.fetch_remote_art} onChange={(event) => updateSetting('fetch_remote_art', event.target.checked)} /></label>
        <label className="switch-row"><span><strong>同时迁移已有 MP3</strong><small>不转码、不改标签。目标保存成功后移除源 MP3；相同文件跳过，同名冲突按设置处理。输出子目录不会重复扫描。</small></span><input type="checkbox" checked={Boolean(settings.migrate_mp3)} onChange={(event) => updateSetting('migrate_mp3', event.target.checked)} /></label>
        <button className="primary-button save-button" type="button" onClick={onSave}>保存设置</button>
      </div>
    </section>
  );
}

function AboutView({ about }) {
  if (!about) return <div className="empty-state"><IconRefresh className="spin" />正在加载项目说明</div>;
  return (
    <section className="page-section about-page">
      <div className="about-copy">
        <h2>项目说明</h2>
        <p>音乐转换中心用于将你合法拥有的本地网易云音乐 NCM 文件还原为文件中原本包含的 MP3、FLAC 或 M4A 音频。MP3 / FLAC 尽可能补齐标题、艺术家、专辑和封面元数据。</p>
        <p>本应用不会把 MP3 重新编码为 FLAC，也不提供音乐下载、账号登录、Cookie 导入或在线访问权限规避功能。</p>
        <p>M4A 按原样还原，保留音质、声道和容器已有标签，暂不补写 NCM 标题和封面，也不会重新编码为 MP3 或 FLAC。</p>
        <p>转换核心基于 ncmdump-go v1.7.5，包含本地格式识别兼容补丁，支持没有 ID3 标签的 MP3 及 M4A 音频容器；补丁不修改 NCM 解密算法。</p>
      </div>
      <div className="about-copy">
        <h2>第三方项目与许可证</h2>
        <div className="license-table">
          {[...(about.dependencies || []), ...(about.references || [])].map((project) => (
            <div className="license-row" key={project.name}>
              <div><a href={project.repository} target="_blank" rel="noreferrer">{project.name}</a><span>{project.version || ''}</span></div>
              <span className={project.role === 'actual_dependency' ? 'role actual' : 'role reference'}>{project.role === 'actual_dependency' ? '实际依赖' : '仅作参考'}</span>
              <span>{project.license}</span>
            </div>
          ))}
        </div>
      </div>
      <div className="usage-boundary">
        <h2>使用边界与免责声明</h2>
        <ul>{(about.usage || []).map((line) => <li key={line}>{line}</li>)}</ul>
      </div>
    </section>
  );
}
