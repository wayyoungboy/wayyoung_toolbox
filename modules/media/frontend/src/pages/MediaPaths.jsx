import { useCallback, useEffect, useRef, useState } from 'react';
import Button from '@douyinfe/semi-ui/lib/es/button';
import Input from '@douyinfe/semi-ui/lib/es/input';
import Modal from '@douyinfe/semi-ui/lib/es/modal';
import Switch from '@douyinfe/semi-ui/lib/es/switch';
import FolderPicker from '../components/FolderPicker';
import NamingSampleFields from '../components/NamingSampleFields';
import NamingPreviewResult from '../components/NamingPreviewResult';
import { api } from '../api';

const directoryFields = [
  { key: 'download_directory', label: '下载来源目录', hint: '未单独设置保存路径的 RSS 新任务使用此目录；入库检查此处的完整下载。旧队列任务和订阅独立路径保持不变。' },
  { key: 'pending_directory', label: '未处理目录', hint: '搬运到此处后自动排队；保存时同时设为 JAVSP 扫描目录。' },
  { key: 'completed_directory', label: '整理完成目录', hint: '独立的完成目录。留空沿用旧相对模板；原地模式不使用此目录。' },
];
const namingFields = [
  ['output_folder_pattern', '输出文件夹模板'], ['path_basename_pattern', '视频文件名模板'],
  ['nfo_basename_pattern', '主 NFO 文件名模板'], ['nfo_title_pattern', 'NFO 标题模板'],
  ['cover_basename_pattern', '封面文件名模板'], ['fanart_basename_pattern', '剧照文件名模板'],
];
const initialSample = { media_id: 'TEST-001', title: '示例影片标题', publish_date: '2026-01-02' };
const splitList = value => value.split(',').map(item => item.trim()).filter(Boolean);

export default function MediaPaths() {
  const [config, setConfig] = useState(null);
  const [busy, setBusy] = useState('load');
  const [error, setError] = useState('');
  const [message, setMessage] = useState('');
  const [requiresReload, setRequiresReload] = useState(false);
  const [sample, setSample] = useState(initialSample);
  const [actressText, setActressText] = useState('示例演员');
  const [filenamesText, setFilenamesText] = useState('TEST-001-C.mp4');
  const [preview, setPreview] = useState(null);
  const [confirmation, setConfirmation] = useState(null);
  const [picker, setPicker] = useState(null);
  const operation = useRef(null);
  const payload = { config, sample: { ...sample, actress: splitList(actressText), filenames: splitList(filenamesText) } };
  const key = JSON.stringify({ ...payload, actressText, filenamesText });
  const current = preview?.key === key ? preview : null;
  const locked = busy === 'save' || busy === 'load' || Boolean(confirmation);

  const readConfig = useCallback(async () => {
    if (operation.current) return;
    const controller = new AbortController();
    operation.current = controller;
    setBusy('load'); setError('');
    try {
      const data = await api.mediaPaths({ signal: controller.signal });
      if (!controller.signal.aborted && operation.current === controller) {
        setConfig(data.config); setPreview(null); setRequiresReload(false); setMessage('');
      }
    } catch (failure) {
      if (!controller.signal.aborted && operation.current === controller) setError(failure.message);
    } finally {
      if (operation.current === controller) { operation.current = null; setBusy(''); }
    }
  }, []);
  useEffect(() => {
    readConfig();
    return () => { operation.current?.abort(); operation.current = null; };
  }, [readConfig]);

  const set = (field, value) => { setConfig(previous => ({ ...previous, [field]: value })); setMessage(''); };
  const loadDirectories = useCallback(path => api.intakeDirectories(path || picker?.path || config?.download_directory), [picker, config?.download_directory]);

  const calculate = async () => {
    if (operation.current || !config || requiresReload) return;
    const controller = new AbortController();
    operation.current = controller;
    setBusy('preview'); setError(''); setMessage('');
    try {
      const data = await api.previewMediaPaths(payload, { signal: controller.signal });
      if (!controller.signal.aborted && operation.current === controller) setPreview({ key, data, sample: payload.sample });
    } catch (failure) {
      if (!controller.signal.aborted && operation.current === controller) {
        setPreview(null); setError(failure.message);
        if (failure.status === 409) setRequiresReload(true);
      }
    } finally {
      if (operation.current === controller) { operation.current = null; setBusy(''); }
    }
  };

  const confirm = async () => {
    if (operation.current || !confirmation) return;
    if (confirmation.type === 'reload') {
      await readConfig(); setConfirmation(null); return;
    }
    const selected = confirmation.preview;
    if (!selected || selected.key !== key || requiresReload) { setConfirmation(null); return; }
    const controller = new AbortController();
    operation.current = controller;
    setBusy('save'); setError(''); setMessage('');
    try {
      const data = await api.updateMediaPaths({ config: selected.data.config, sample: selected.sample, preview_digest: selected.data.preview_digest }, { signal: controller.signal });
      if (!controller.signal.aborted && operation.current === controller) {
        setConfig(data.config); setPreview(null); setMessage('目录与命名已保存，不会自动执行任务。');
      }
    } catch (failure) {
      if (!controller.signal.aborted && operation.current === controller) {
        setPreview(null);
        setRequiresReload(failure.status !== 400);
        setError(failure.status === 400 ? failure.message : `${failure.message}。请读取最新配置核对保存结果；当前草稿仍保留。`);
      }
    } finally {
      if (operation.current === controller) { operation.current = null; setBusy(''); setConfirmation(null); }
    }
  };

  return <section className="page embedded-page media-paths-page">
    <header className="page-header"><div><h2>目录与命名</h2><p>一次配置下载入库、待整理与完成目录。先预览最终路径，再确认保存。</p></div>
      {config ? <Button htmlType="button" disabled={Boolean(busy)} onClick={() => setConfirmation({ type: 'reload' })}>读取最新配置</Button> : null}
    </header>
    {error ? <p className="notice-box danger-text" role="alert">{error}</p> : null}
    {message ? <p className="notice-box success-text" role="status">{message}</p> : null}
    {!config ? <div className="center-state">{busy ? '正在读取目录配置…' : <Button htmlType="button" onClick={readConfig}>重试读取</Button>}</div> : <>
      <ol className="media-path-flow">
        {directoryFields.map((field, index) => <li key={field.key}>
          <span className="path-step">{index + 1}</span>
          <label className="settings-field"><span>{field.label}</span><Input aria-label={field.label} value={config[field.key]} disabled={locked} onChange={value => set(field.key, value)} /></label>
          <Button htmlType="button" disabled={locked} aria-label={'选择' + field.label} onClick={() => setPicker({ ...field, path: config[field.key] || config.pending_directory })}>选择文件夹</Button>
          <p>{field.hint}</p>
        </li>)}
      </ol>
      <p className="muted-text">目录必须已存在并已授予应用访问权限。保存不移动现有文件，不会修改已排队任务的来源或已确认整理计划。</p>
      <label className="switch-row setting-switch"><span><strong>整理时移动视频</strong><small>关闭后在来源旁生成元数据，不把视频移入完成目录。</small></span><Switch aria-label="整理时移动视频" checked={config.move_files} disabled={locked} onChange={value => set('move_files', value)} /></label>
      <section className="settings-section">
        <div className="settings-title"><h2>命名规则</h2><p>文件名不含扩展名。支持原有模板变量，例如 {'{num}'}、{'{title}'}、{'{actress}'}、{'{year}'}；其余高级规则仍在 JAVSP 设置中。</p></div>
        <div className="settings-grid">{namingFields.map(([field, label]) => <label className="settings-field" key={field}><span>{label}</span><Input aria-label={label} value={config[field]} disabled={locked} onChange={value => set(field, value)} /></label>)}</div>
      </section>
      <section className="settings-section naming-preview">
        <div className="settings-title"><h2>预览与保存</h2><p>使用示例元数据计算，不扫描、不联网、不翻译、不写文件。更改任何规则或示例后，需要重新预览。</p></div>
        <div className="settings-grid">
          <NamingSampleFields sample={sample} actressText={actressText} filenamesText={filenamesText} onChange={(field, value) => setSample(previous => ({ ...previous, [field]: value }))} onActressChange={setActressText} onFilenamesChange={setFilenamesText} disabled={locked} />
          {preview && !current ? <p className="full-field muted-text">配置或示例已变更，请重新预览。</p> : null}
          {requiresReload ? <p className="full-field folder-picker-warning">配置版本或保存结果需要重新核对，请先读取最新配置。</p> : null}
          <div className="full-field inline-actions">
            <Button htmlType="button" aria-label="预览最终路径" disabled={Boolean(busy) || requiresReload} loading={busy === 'preview'} onClick={calculate}>预览最终路径</Button>
            <Button htmlType="button" theme="solid" type="primary" disabled={!current || Boolean(busy) || requiresReload} onClick={() => setConfirmation({ type: 'save', preview: current })}>确认并保存</Button>
          </div>
          {current ? <NamingPreviewResult data={current.data.preview} /> : null}
        </div>
      </section>
    </>}
    {picker ? <FolderPicker key={picker.key} visible title={'选择' + picker.label} loadDirectories={loadDirectories} onCancel={() => setPicker(null)} onSelect={path => { set(picker.key, path); setPicker(null); }} /> : null}
    <Modal visible={Boolean(confirmation)} title={confirmation?.type === 'reload' ? '读取最新目录配置？' : '保存目录与命名配置？'}
      okText={confirmation?.type === 'reload' ? '确认读取' : '确认保存'} cancelText="取消"
      onOk={confirm} onCancel={() => { if (!operation.current) setConfirmation(null); }}
      confirmLoading={busy === 'save' || busy === 'load'} okButtonProps={{ disabled: Boolean(busy), 'aria-label': confirmation?.type === 'reload' ? '确认读取' : '确认保存' }} cancelButtonProps={{ disabled: Boolean(busy), 'aria-label': '取消' }}
      closable={!busy} closeOnEsc={!busy} maskClosable={false}>
      {confirmation?.type === 'reload' ? <p>这会用服务器当前配置替换未保存的目录与命名草稿。取消则保留当前输入。</p> : <p>确认使用刚预览的目录与命名规则？两个模块将一起保存，不会扫描、投递、整理或移动现有文件。</p>}
    </Modal>
  </section>;
}
