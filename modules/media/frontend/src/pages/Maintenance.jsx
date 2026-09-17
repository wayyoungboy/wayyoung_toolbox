import { useEffect, useMemo, useState } from 'react';
import Button from '@douyinfe/semi-ui/lib/es/button';
import Input from '@douyinfe/semi-ui/lib/es/input';
import InputNumber from '@douyinfe/semi-ui/lib/es/inputNumber';
import Modal from '@douyinfe/semi-ui/lib/es/modal';
import Select from '@douyinfe/semi-ui/lib/es/select';
import Switch from '@douyinfe/semi-ui/lib/es/switch';
import Table from '@douyinfe/semi-ui/lib/es/table';
import Toast from '@douyinfe/semi-ui/lib/es/toast';
import { IconDelete, IconFolderOpen, IconSave, IconSearch } from '@douyinfe/semi-icons';
import { api, formatDate, formatDuration } from '../api';
import FolderPicker from '../components/FolderPicker';

function formatBytes(value) {
  if (!Number.isFinite(value)) return '--';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let size = value;
  let unit = 0;
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024;
    unit += 1;
  }
  return `${size.toFixed(unit ? 2 : 0)} ${units[unit]}`;
}

export default function Maintenance() {
  const [data, setData] = useState(null);
  const [config, setConfig] = useState(null);
  const [pickerVisible, setPickerVisible] = useState(false);
  const [saving, setSaving] = useState(false);
  const [scanning, setScanning] = useState(false);
  const [previewing, setPreviewing] = useState(false);
  const [cleaning, setCleaning] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [duplicateResult, setDuplicateResult] = useState(null);
  const [selectedDuplicateKeys, setSelectedDuplicateKeys] = useState([]);
  const [emptyResult, setEmptyResult] = useState(null);
  const [autoResult, setAutoResult] = useState(null);
  const [autoBusy, setAutoBusy] = useState(false);

  const load = async () => {
    try {
      const next = await api.maintenance();
      setData(next);
      setConfig(next.config);
    } catch (error) { Toast.error(error.message); }
  };
  useEffect(() => { load(); }, []);

  const updateConfig = (changes) => {
    setConfig((current) => ({ ...current, ...changes }));
    setEmptyResult(null);
    setAutoResult(null);
  };

  const save = async (showToast = true) => {
    setSaving(true);
    try {
      const saved = await api.updateMaintenance(config);
      setConfig(saved);
      setData((current) => ({ ...current, config: saved }));
      if (showToast) Toast.success('媒体维护设置已保存');
      return saved;
    } catch (error) {
      Toast.error(error.message);
      return null;
    } finally { setSaving(false); }
  };

  const scanDuplicates = async () => {
    setScanning(true);
    try {
      const saved = await save(false);
      if (!saved) return;
      const result = await api.scanDuplicates(saved.root_directory);
      setDuplicateResult(result);
      setSelectedDuplicateKeys([]);
      Toast.success(`已扫描 ${result.scanned_videos} 个视频，发现 ${result.groups.length} 组同番号文件`);
      await load();
    } catch (error) { Toast.error(error.message); }
    finally { setScanning(false); }
  };

  const automaticDuplicates = async (execute = false) => {
    setAutoBusy(true);
    try {
      if (!await save(false)) return;
      const result = execute ? await api.cleanupAutomaticDuplicates() : await api.previewAutomaticDuplicates();
      setAutoResult(result);
      if (execute) {
        setDuplicateResult(null);
        setSelectedDuplicateKeys([]);
        Toast.success(`已永久删除 ${result.deleted} 个重复视频，合计 ${formatBytes(result.reclaimed_bytes)}`);
      }
      await load();
    } catch (error) { Toast.error(error.message); await load(); }
    finally { setAutoBusy(false); }
  };

  const confirmAutomaticCleanup = () => Modal.confirm({
    title: '按番号保留最大文件',
    content: '将重新扫描当前维护目录，永久删除同番号较小或同大小的多余视频，每组保留一份最大文件。分段影片及近期修改的文件跳过；不删除 NFO 和图片。',
    okText: '立即清理', cancelText: '取消',
    onOk: () => automaticDuplicates(true),
  });

  const previewEmpty = async () => {
    setPreviewing(true);
    try {
      const saved = await save(false);
      if (!saved) return;
      const result = await api.previewEmptyDirectories();
      setEmptyResult(result);
      Toast.success(`检查完成：发现 ${result.found} 个符合条件的空目录`);
    } catch (error) { Toast.error(error.message); }
    finally { setPreviewing(false); }
  };

  const cleanupEmpty = () => {
    if (!emptyResult) {
      Toast.warning('请先预览空目录');
      return;
    }
    Modal.confirm({
      title: `删除 ${emptyResult.found} 个空目录？`,
      content: `仅处理 ${config.root_directory} 内、已空置至少 ${config.minimum_age_hours} 小时的目录。执行前服务会再次确认目录为空；视频文件不会被删除。`,
      okText: '确认删除空目录',
      okButtonProps: { type: 'danger' },
      onOk: async () => {
        setCleaning(true);
        try {
          const result = await api.cleanupEmptyDirectories();
          setEmptyResult(result);
          Toast.success(`已删除 ${result.removed} 个空目录，失败 ${result.failed} 个`);
          await load();
        } catch (error) { Toast.error(error.message); }
        finally { setCleaning(false); }
      },
    });
  };

  const duplicateFiles = useMemo(() => (duplicateResult?.groups || []).flatMap((group) => (
    group.files.map((file, index) => ({
      ...file,
      row_key: `${group.media_id}-${file.path}`,
      media_id: group.media_id,
      match_type: group.match_type,
      group_index: index,
      group_size: group.files.length,
    }))
  )), [duplicateResult]);

  const duplicateColumns = [
    {
      title: '番号', dataIndex: 'media_id', width: 130,
      render: (value, item) => <div><strong>{value}</strong>{item.group_index === 0 ? <div className="cell-secondary">共 {item.group_size} 个文件</div> : null}</div>,
    },
    {
      title: '判断', dataIndex: 'match_type', width: 150,
      render: (value) => value === 'fingerprint' ? <span className="danger-text">采样指纹相同</span> : '同番号，内容待核对',
    },
    { title: '大小', dataIndex: 'size', width: 120, render: formatBytes },
    { title: '文件路径', dataIndex: 'path', render: (value) => <span className="maintenance-path" title={value}>{value}</span> },
  ];

  const selectedDuplicateFiles = useMemo(() => {
    const keys = new Set(selectedDuplicateKeys);
    return duplicateFiles.filter((file) => keys.has(file.row_key));
  }, [duplicateFiles, selectedDuplicateKeys]);

  const selectFingerprintCopies = () => {
    const keys = (duplicateResult?.groups || []).flatMap((group) => (
      group.match_type === 'fingerprint'
        ? group.files.slice(1).map((file) => `${group.media_id}-${file.path}`)
        : []
    ));
    setSelectedDuplicateKeys(keys);
    if (!keys.length) Toast.warning('没有可自动选择的采样指纹相同副本');
  };

  const deleteDuplicates = () => {
    if (!selectedDuplicateFiles.length) {
      Toast.warning('请先选择要永久删除的视频');
      return;
    }
    const bytes = selectedDuplicateFiles.reduce((total, file) => total + file.size, 0);
    Modal.confirm({
      title: `永久删除 ${selectedDuplicateFiles.length} 个视频？`,
      content: `将立即释放约 ${formatBytes(bytes)}。此操作不可恢复；服务会重新扫描并确认每组至少保留一个文件，文件发生变化时会拒绝执行。`,
      okText: '确认永久删除',
      okButtonProps: { type: 'danger' },
      onOk: async () => {
        setDeleting(true);
        try {
          const files = selectedDuplicateFiles.map(({ path, size, fingerprint }) => ({ path, size, fingerprint: fingerprint || '' }));
          const result = await api.deleteDuplicateVideos(files);
          setSelectedDuplicateKeys([]);
          Toast.success(`已永久删除 ${result.deleted} 个视频，释放 ${formatBytes(result.reclaimed_bytes)}`);
          const refreshed = await api.scanDuplicates(config.root_directory);
          setDuplicateResult(refreshed);
          await load();
        } catch (error) { Toast.error(error.message); }
        finally { setDeleting(false); }
      },
    });
  };

  const runColumns = [
    { title: '开始时间', dataIndex: 'started_at', width: 145, render: formatDate },
    { title: '任务', dataIndex: 'kind', width: 130, render: (value) => value === 'duplicates' ? '重复视频扫描' : value === 'duplicate_cleanup' ? '按大小自动去重' : value === 'duplicate_delete' ? '永久删除重复视频' : value === 'intake' ? '下载完成入库' : '空目录清理' },
    { title: '触发', dataIndex: 'trigger_type', width: 90, render: (value) => value === 'schedule' ? '定时' : '手动' },
    { title: '状态', dataIndex: 'status', width: 90, render: (value) => <span className={value === 'success' ? 'success-text' : value === 'failed' ? 'danger-text' : ''}>{value === 'success' ? '成功' : value === 'failed' ? '失败' : '运行中'}</span> },
    { title: '扫描/发现/处理', width: 150, render: (_, item) => `${item.scanned_count} / ${item.found_count} / ${item.action_count}` },
    { title: '耗时', width: 90, render: (_, item) => formatDuration(item.started_at, item.finished_at) },
    { title: '错误', dataIndex: 'error', render: (value) => value || '--' },
  ];

  if (!config) return <div className="center-state">正在读取媒体维护设置…</div>;
  return (
    <section className="page maintenance-page">
      <header className="page-header">
        <div><h1>媒体维护</h1><p>检查同番号视频，并安全清理长期为空的旧目录</p></div>
        <Button theme="solid" type="primary" icon={<IconSave />} loading={saving} onClick={() => save()}>保存设置</Button>
      </header>

      <div className="notice-box maintenance-notice">
        <strong>同番号保留最大文件，大小相同只留一份</strong>
        <span>例如 MIDA-734、MIDA-734-C 视为同一番号。自动去重按体积选择，不要求内容指纹相同；同大小按路径顺序保留一份。删除不可恢复，NFO 和图片保留。</span>
      </div>

      <div className="maintenance-directory-bar">
        <div><strong>维护根目录</strong><span>重复扫描和空目录清理都限定在此目录</span></div>
        <Input aria-label="媒体维护根目录" value={config.root_directory} onChange={(rootDirectory) => updateConfig({ root_directory: rootDirectory })} />
        <Button icon={<IconFolderOpen />} onClick={() => setPickerVisible(true)}>选择文件夹</Button>
      </div>

      <div className="maintenance-grid">
        <section className="maintenance-card">
          <div className="section-heading"><div><h2>重复视频检查</h2><p>按标准化番号分组，再使用文件大小和三段采样指纹辅助判断</p></div><Button icon={<IconSearch />} loading={scanning} onClick={scanDuplicates}>开始扫描</Button></div>
          <div className="maintenance-summary">{duplicateResult ? `扫描 ${duplicateResult.scanned_videos} 个视频，发现 ${duplicateResult.groups.length} 组` : '尚未扫描'}</div>
        </section>

        <section className="maintenance-card">
          <div className="section-heading"><div><h2>空目录清理</h2><p>先预览，删除时会再次确认目录仍为空</p></div></div>
          <div className="maintenance-cleanup-actions">
            <label>最短空置时间 <InputNumber min={1} max={8760} value={config.minimum_age_hours} onChange={(minimumAgeHours) => updateConfig({ minimum_age_hours: minimumAgeHours })} suffix="小时" /></label>
            <Button icon={<IconSearch />} loading={previewing} onClick={previewEmpty}>预览空目录</Button>
            <Button type="danger" icon={<IconDelete />} loading={cleaning} disabled={!emptyResult?.found} onClick={cleanupEmpty}>删除已预览空目录</Button>
          </div>
          <div className="maintenance-summary">{emptyResult ? `扫描 ${emptyResult.scanned_dirs} 个目录，符合条件 ${emptyResult.found} 个，已删除 ${emptyResult.removed} 个` : '尚未预览'}</div>
        </section>
      </div>

      <section className="maintenance-card">
        <div className="section-heading"><div><h2>自动删除重复视频</h2><p>仅处理完整番号的普通视频；分段影片、STRM、空文件、近期修改的文件跳过。执行前再次验证保留文件。</p></div></div>
        <div className="inline-actions">
          <Button icon={<IconSearch />} loading={autoBusy} onClick={() => automaticDuplicates(false)}>预览保留与删除</Button>
          <Button type="danger" icon={<IconDelete />} loading={autoBusy} onClick={confirmAutomaticCleanup}>保留最大文件并立即清理</Button>
        </div>
        {autoResult ? <Table rowKey="media_id" dataSource={autoResult.groups} pagination={{ pageSize: 10 }} columns={[
          { title: '番号', dataIndex: 'media_id', width: 140 },
          { title: '保留文件', render: (_, group) => <span className="maintenance-path">{group.keep.path}（{formatBytes(group.keep.size)}）</span> },
          { title: '删除候选 / 跳过原因', render: (_, group) => group.skip_reason || group.remove.map((file) => <div className="maintenance-path" key={file.path}>{file.path}（{formatBytes(file.size)}）{autoResult.files.includes(file.path) ? ' · 已删除' : ''}</div>) },
        ]} empty="未发现符合条件的同番号重复影片" /> : null}
      </section>

      {duplicateResult ? <div className="maintenance-results"><div className="section-heading"><div><h2>重复视频结果</h2><span className="muted-text">已选择 {selectedDuplicateFiles.length} 个；删除不可恢复</span></div><div className="inline-actions"><Button onClick={selectFingerprintCopies}>选择指纹相同副本</Button><Button type="danger" icon={<IconDelete />} loading={deleting} disabled={!selectedDuplicateFiles.length} onClick={deleteDuplicates}>永久删除所选</Button></div></div><Table rowKey="row_key" columns={duplicateColumns} dataSource={duplicateFiles} rowSelection={{ selectedRowKeys: selectedDuplicateKeys, onChange: setSelectedDuplicateKeys }} pagination={{ pageSize: 30 }} scroll={{ x: 850 }} empty="未发现同番号视频" /></div> : null}
      {emptyResult?.directories?.length ? <div className="maintenance-empty-list"><div className="section-heading"><h2>{emptyResult.removed ? '本次处理目录' : '待清理空目录'}</h2><span className="muted-text">{emptyResult.directory}</span></div><ul>{emptyResult.directories.map((path) => <li key={path}>{path}</li>)}</ul></div> : null}

      <section className="maintenance-schedule">
        <div className="section-heading"><div><h2>定时媒体维护</h2><p>两个功能独立开关，共用执行时间；修改后点击「保存设置」生效。</p></div></div>
        <div className="settings-grid">
          <label className="settings-field"><span>自动删除重复视频（保留最大的一份）</span><Switch aria-label="自动删除重复视频" checked={Boolean(config.duplicate_cleanup_enabled)} onChange={(enabled) => updateConfig({ duplicate_cleanup_enabled: enabled })} /></label>
          <label className="settings-field"><span>自动清理空目录</span><Switch aria-label="自动清理空目录" checked={config.cleanup_enabled} onChange={(enabled) => updateConfig({ cleanup_enabled: enabled })} /></label>
          <label className="settings-field"><span>文件/目录至少多久未修改才可清理</span><InputNumber min={1} max={8760} value={config.minimum_age_hours} onChange={(value) => updateConfig({ minimum_age_hours: value })} suffix="小时" /></label>
        </div>
        <div className="settings-grid">
          <label className="settings-field"><span>执行方式</span><Select value={config.schedule_type} onChange={(scheduleType) => updateConfig({ schedule_type: scheduleType })} optionList={[{ value: 'daily', label: '每天定时' }, { value: 'interval', label: '按间隔执行' }]} /></label>
          {config.schedule_type === 'daily'
            ? <label className="settings-field"><span>每天执行时间</span><Input value={config.schedule_time} onChange={(scheduleTime) => updateConfig({ schedule_time: scheduleTime })} placeholder="05:00" /></label>
            : <label className="settings-field"><span>执行间隔</span><InputNumber min={15} max={10080} value={config.schedule_minutes} onChange={(scheduleMinutes) => updateConfig({ schedule_minutes: scheduleMinutes })} suffix="分钟" style={{ width: '100%' }} /></label>}
          <div className="settings-field"><span>上次执行</span><div>{formatDate(config.last_run_at)}</div></div>
          <div className="settings-field"><span>下次执行</span><div>{config.cleanup_enabled || config.duplicate_cleanup_enabled ? formatDate(config.next_run_at) : '未启用'}</div></div>
        </div>
      </section>

      <div className="maintenance-results"><div className="section-heading"><h2>媒体维护运行记录</h2><span className="muted-text">最多保留最近 100 次</span></div><Table rowKey="id" columns={runColumns} dataSource={data?.runs || []} expandedRowRender={(item) => <pre style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}>{(item.details || []).join('\n') || '无详细记录'}</pre>} pagination={{ pageSize: 20 }} empty="暂无运行记录" /></div>

      <FolderPicker
        visible={pickerVisible}
        title="选择媒体维护根目录"
        loadDirectories={api.maintenanceDirectories}
        onCancel={() => setPickerVisible(false)}
        onSelect={(path) => { updateConfig({ root_directory: path }); setPickerVisible(false); }}
      />
    </section>
  );
}
