import { useEffect, useState } from 'react';
import Button from '@douyinfe/semi-ui/lib/es/button';
import Input from '@douyinfe/semi-ui/lib/es/input';
import InputNumber from '@douyinfe/semi-ui/lib/es/inputNumber';
import Modal from '@douyinfe/semi-ui/lib/es/modal';
import Switch from '@douyinfe/semi-ui/lib/es/switch';
import Table from '@douyinfe/semi-ui/lib/es/table';
import Toast from '@douyinfe/semi-ui/lib/es/toast';
import { IconFolderOpen, IconPlay, IconSave, IconSearch } from '@douyinfe/semi-icons';
import { api, formatDate, formatDuration } from '../api';
import FolderPicker from '../components/FolderPicker';

export default function Intake({ embedded = false }) {
  const [data, setData] = useState(null);
  const [config, setConfig] = useState(null);
  const [savedConfig, setSavedConfig] = useState(null);
  const [preview, setPreview] = useState(null);
  const [saving, setSaving] = useState(false);
  const [previewing, setPreviewing] = useState(false);
  const [running, setRunning] = useState(false);
  const [confirmRun, setConfirmRun] = useState(false);
  const [picker, setPicker] = useState('');
  const busy = saving || previewing || running;

  const load = async () => {
    try {
      const value = await api.intake();
      setData(value);
      setConfig(value.config);
      setSavedConfig(value.config);
    } catch (error) { Toast.error(error.message); }
  };
  useEffect(() => { load(); }, []);

  const set = (key, value) => setConfig((current) => ({ ...current, [key]: value }));
  const save = async (showToast = true) => {
    const editable = ['enabled', 'source_directory', 'destination_directory', 'schedule_minutes', 'run_javsp'];
    if (!showToast && savedConfig && editable.every(key => config[key] === savedConfig[key])) return config;
    setSaving(true);
    try {
      const value = await api.updateIntake(config);
      setConfig(value);
      setSavedConfig(value);
      if (showToast) Toast.success('下载入库设置已保存');
      return value;
    } catch (error) {
      Toast.error(error.message);
      return null;
    } finally { setSaving(false); }
  };
  const inspect = async () => {
    setPreviewing(true);
    try {
      const confirmed = await save(false);
      if (!confirmed) return;
      const value = await api.previewIntake(confirmed.revision);
      setPreview(value);
      const summary = `检查 ${value.scanned} 个 qBittorrent 任务，发现 ${value.candidates.length} 个已完成任务`;
      if (value.failed > 0 || value.errors?.length) Toast.warning(`检查部分失败：${summary}；请查看下方具体错误`);
      else Toast.success(summary);
    } catch (error) { Toast.error(error.message); }
    finally { setPreviewing(false); }
  };
  const run = async () => {
      if (busy) return;
      setRunning(true);
      try {
        const confirmed = await save(false);
        if (!confirmed) return;
        const value = await api.runIntake(confirmed.revision);
        setPreview(value);
        const javsp = value.javsp_queued ? `，${value.javsp_queued} 个影片任务已排队整理` : value.javsp_error ? `，JAVSP 未启动：${value.javsp_error}` : '';
        const summary = `已移动 ${value.moved} 个，清理重复 ${value.duplicates_cleaned} 个，删除任务记录 ${value.tasks_deleted} 个${javsp}`;
        if (value.failed > 0 || value.errors?.length) Toast.warning(`入库部分失败：${Math.max(value.failed || 0, value.errors?.length || 0)} 项检查或处理失败；${summary}`);
        else Toast.success(summary);
        await load();
      } catch (error) { Toast.error(error.message); }
      finally { setRunning(false); setConfirmRun(false); }
  };

  const candidateColumns = [
    { title: '任务', dataIndex: 'name', width: 220 },
    { title: '状态', dataIndex: 'state', width: 120 },
    { title: '来源', dataIndex: 'source', render: (value, item) => <div><span className="maintenance-path" title={value}>{value}</span>{item.source_root ? <div className="queue-footnote">登记边界：{item.source_root}</div> : null}</div> },
    { title: '目标', dataIndex: 'destination', render: (value) => <span className="maintenance-path" title={value}>{value}</span> },
  ];
  const runColumns = [
    { title: '开始时间', dataIndex: 'started_at', width: 150, render: formatDate },
    { title: '触发', dataIndex: 'trigger_type', width: 90, render: (value) => value === 'schedule' ? '定时' : '手动' },
    { title: '状态', dataIndex: 'status', width: 90, render: (value) => <span className={value === 'success' ? 'success-text' : value === 'failed' ? 'danger-text' : ''}>{value === 'success' ? '成功' : value === 'partial' ? '部分失败' : value === 'failed' ? '失败' : '运行中'}</span> },
    { title: '扫描/候选/完成', width: 150, render: (_, item) => `${item.scanned_count} / ${item.found_count} / ${item.action_count}` },
    { title: '耗时', width: 90, render: (_, item) => formatDuration(item.started_at, item.finished_at) },
    { title: '错误', dataIndex: 'error', render: (value) => value || '--' },
  ];

  if (!config) return <div className="center-state">正在读取下载入库设置…</div>;
  return (
    <section className={`page intake-page${embedded ? ' embedded-page' : ''}`}>
      <header className="page-header">
        <div>{embedded ? <h2>下载与入库</h2> : <h1>下载完成入库</h1>}<p>把 qBittorrent 已完成内容移动到 JAVSP 未处理目录</p></div>
        <Button theme="solid" type="primary" icon={<IconSave />} disabled={busy} loading={saving} onClick={() => save()}>保存设置</Button>
      </header>

      <div className="notice-box maintenance-notice">
        <strong>只处理已完整下载且通过来源边界检查的任务</strong>
        <span>新发现的任务使用当前下载来源；已登记任务仍按各自保存的来源边界检查，不会扫描旧目录中的其他文件。搬运成功并复核后才删除 qBittorrent 任务记录；目标存在时必须复核内容，不能仅凭同名删除来源。</span>
      </div>

      <section className="maintenance-schedule intake-config">
        <div className="section-heading"><div><h2>目录与自动任务</h2><p>原生服务直接操作 NAS 文件，不再通过 SSH 回连本机</p></div><Switch disabled={busy} checked={config.enabled} onChange={(value) => set('enabled', value)} /></div>
        <div className="intake-directory-row">
          <label className="settings-field"><span>qBittorrent 下载目录</span><Input disabled={busy} value={config.source_directory} onChange={(value) => set('source_directory', value)} /></label>
          <Button disabled={busy} icon={<IconFolderOpen />} onClick={() => setPicker('source_directory')}>选择文件夹</Button>
        </div>
        <div className="intake-directory-row">
          <label className="settings-field"><span>JAVSP 未处理目录</span><Input disabled={busy} value={config.destination_directory} onChange={(value) => set('destination_directory', value)} /></label>
          <Button disabled={busy} icon={<IconFolderOpen />} onClick={() => setPicker('destination_directory')}>选择文件夹</Button>
        </div>
        <div className="settings-grid intake-settings-grid">
          <label className="settings-field"><span>检查间隔</span><InputNumber disabled={busy} min={5} max={10080} value={config.schedule_minutes} onChange={(value) => set('schedule_minutes', value)} suffix="分钟" style={{ width: '100%' }} /></label>
          <label className="switch-row setting-switch"><span><strong>搬运后排队整理</strong><small>符合条件的影片进入 JAVSP 队列，不代表已整理成功</small></span><Switch disabled={busy} checked={config.run_javsp} onChange={(value) => set('run_javsp', value)} /></label>
          <div className="settings-field"><span>上次执行</span><div>{formatDate(config.last_run_at)}</div></div>
          <div className="settings-field"><span>下次执行</span><div>{config.enabled ? formatDate(config.next_run_at) : '未启用'}</div></div>
        </div>
        <div className="inline-actions intake-actions"><Button disabled={busy} icon={<IconSearch />} loading={previewing} onClick={inspect}>检查已完成任务</Button><Button disabled={busy} theme="solid" type="primary" icon={<IconPlay />} loading={running} onClick={() => setConfirmRun(true)}>立即搬运并入库</Button></div>
      </section>

      {preview ? <div className="maintenance-results"><div className="section-heading"><h2>本次检查结果</h2><span className="muted-text">扫描 {preview.scanned} 个任务，候选 {preview.candidates.length} 个</span></div>
        {preview.failed > 0 || preview.errors?.length ? <div role="alert" className="error-box intake-issues"><strong>有 {Math.max(preview.failed || 0, preview.errors?.length || 0)} 项检查或处理未完成</strong><ul>{(preview.errors || []).map((error, index) => <li key={index}>{error}</li>)}</ul><p>未通过检查的任务不会因本次检查而开始搬运，请核对错误后再处理。</p></div> : null}
        <Table rowKey="hash" columns={candidateColumns} dataSource={preview.candidates} pagination={{ pageSize: 20 }} scroll={{ x: 1100 }} empty="没有符合条件的已完成任务" /></div> : null}
      <div className="maintenance-results"><div className="section-heading"><h2>下载入库运行记录</h2><span className="muted-text">最近 20 次</span></div><Table rowKey="id" columns={runColumns} dataSource={data?.runs || []} pagination={{ pageSize: 20 }} empty="暂无下载入库记录" /></div>

      <FolderPicker
        visible={Boolean(picker)}
        title={picker === 'source_directory' ? '选择 qBittorrent 下载目录' : '选择 JAVSP 未处理目录'}
        loadDirectories={api.intakeDirectories}
        onCancel={() => setPicker('')}
        onSelect={(path) => { set(picker, path); setPicker(''); }}
      />
      <Modal visible={confirmRun} title="搬运已完成的 qBittorrent 内容？" okText="确认搬运" cancelText="取消"
        onOk={run} onCancel={() => { if (!busy) setConfirmRun(false); }} confirmLoading={running}
        okButtonProps={{ disabled: busy, 'aria-label': '确认搬运' }} cancelButtonProps={{ disabled: busy, 'aria-label': '取消' }} closable={!busy} closeOnEsc={!busy} maskClosable={false}>
        <p>检查“{config.source_directory}”下已完整下载的任务；已登记任务仍按各自保存的来源边界检查。通过检查后移动到“{config.destination_directory}”，成功并复核后删除 qBittorrent 任务记录，不删除目标视频。符合配置的影片会排队整理。</p>
      </Modal>
    </section>
  );
}
