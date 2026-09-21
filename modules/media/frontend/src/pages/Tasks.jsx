import { useEffect, useMemo, useState } from 'react';
import Button from '@douyinfe/semi-ui/lib/es/button';
import Input from '@douyinfe/semi-ui/lib/es/input';
import InputNumber from '@douyinfe/semi-ui/lib/es/inputNumber';
import Modal from '@douyinfe/semi-ui/lib/es/modal';
import Select from '@douyinfe/semi-ui/lib/es/select';
import SideSheet from '@douyinfe/semi-ui/lib/es/sideSheet';
import Switch from '@douyinfe/semi-ui/lib/es/switch';
import Table from '@douyinfe/semi-ui/lib/es/table';
import Toast from '@douyinfe/semi-ui/lib/es/toast';
import { IconDelete, IconEdit, IconPlay, IconPlus, IconRefresh, IconSearch } from '@douyinfe/semi-icons';
import { api, formatDate } from '../api';
import CompletedLibrary from '../components/CompletedLibrary';

const emptyTask = {
  name: '', feed_url: '', enabled: true, schedule_type: 'interval',
  schedule_minutes: 60, schedule_time: '03:00',
  include_all: [], include_any: [], exclude_any: [], extractor: 'rmdown',
  proxy_url: '', qb_category: '', save_path: '',
};

const listToText = (value) => (Array.isArray(value) ? value.join(', ') : value || '');
const textToList = (value) => String(value || '').split(/[,，\n]/).map((item) => item.trim()).filter(Boolean);

function Field({ label, hint, required, children }) {
  return (
    <label className="form-field">
      <span className="field-label">{label}{required ? <b>*</b> : null}</span>
      {children}
      {hint ? <span className="field-hint">{hint}</span> : null}
    </label>
  );
}

export default function Tasks({ embedded = false }) {
  const [tasks, setTasks] = useState([]);
  const [loading, setLoading] = useState(true);
  const [query, setQuery] = useState('');
  const [filter, setFilter] = useState('all');
  const [drawer, setDrawer] = useState(false);
  const [form, setForm] = useState(emptyTask);
  const [editingId, setEditingId] = useState(null);
  const [saving, setSaving] = useState(false);
  const [previewing, setPreviewing] = useState(false);
  const [preview, setPreview] = useState(null);
  const [runningAction, setRunningAction] = useState(null);

  const load = async () => {
    setLoading(true);
    try {
      setTasks((await api.tasks()).items);
    } catch (error) {
      Toast.error(error.message);
    } finally {
      setLoading(false);
    }
  };
  useEffect(() => { load(); }, []);

  const visible = useMemo(() => tasks.filter((task) => {
    const matchQuery = !query || `${task.name} ${task.feed_url}`.toLowerCase().includes(query.toLowerCase());
    const matchFilter = filter === 'all' || (filter === 'enabled' ? task.enabled : !task.enabled);
    return matchQuery && matchFilter;
  }), [tasks, query, filter]);

  const openNew = () => {
    setEditingId(null); setForm(emptyTask); setPreview(null); setDrawer(true);
  };
  const openEdit = (task) => {
    setEditingId(task.id); setForm({ ...task }); setPreview(null); setDrawer(true);
  };
  const payload = () => ({
    ...form,
    include_all: textToList(listToText(form.include_all)),
    include_any: textToList(listToText(form.include_any)),
    exclude_any: textToList(listToText(form.exclude_any)),
  });
  const set = (key, value) => setForm((current) => ({ ...current, [key]: value }));

  const save = async () => {
    setSaving(true);
    try {
      if (editingId) await api.updateTask(editingId, payload());
      else await api.createTask(payload());
      Toast.success(editingId ? '订阅已更新' : '订阅已创建');
      setDrawer(false);
      await load();
    } catch (error) {
      Toast.error(error.message);
    } finally {
      setSaving(false);
    }
  };

  const doPreview = async () => {
    setPreviewing(true);
    try {
      const result = await api.previewTask(payload());
      setPreview(result);
      Toast.success(`已读取 ${result.total} 条，匹配 ${result.matched} 条`);
    } catch (error) {
      Toast.error(error.message);
    } finally {
      setPreviewing(false);
    }
  };

  const remove = (task) => Modal.confirm({
    title: '删除 RSS 订阅？',
    content: `将删除“${task.name}”及其运行记录，不会删除任何下载文件。`,
    okText: '删除',
    okButtonProps: { type: 'danger' },
    onOk: async () => {
      await api.deleteTask(task.id);
      Toast.success('订阅已删除');
      await load();
    },
  });

  const toggle = async (task, enabled) => {
    try {
      const updated = await api.updateTask(task.id, { ...task, enabled });
      setTasks((current) => current.map((item) => item.id === task.id ? updated : item));
    } catch (error) {
      Toast.error(error.message);
    }
  };

  const execute = async (task, mode) => {
    setRunningAction({ taskId: task.id, mode });
    try {
      if (mode === 'preview') await api.previewRunTask(task.id);
      else await api.submitTask(task.id);
      Toast.success(mode === 'preview' ? '预览任务已启动，不会投递下载' : '投递任务已启动，可在运行记录中查看进度');
      await load();
    } catch (error) {
      Toast.error(error.message);
    } finally {
      setRunningAction(null);
    }
  };

  const submit = (task) => Modal.confirm({
    title: '立即采集并投递？',
    content: `将忽略全局预览模式，执行“${task.name}”并把尚未处理的匹配项投递到 qBittorrent。`,
    okText: '确认投递',
    onOk: () => execute(task, 'submit'),
  });

  const columns = [
    { title: '任务名称', dataIndex: 'name', render: (name, task) => <div><strong>{name}</strong><div className="cell-secondary">{task.feed_url}</div></div> },
    { title: '状态', dataIndex: 'enabled', width: 100, render: (enabled, task) => <Switch size="small" checked={enabled} onChange={(value) => toggle(task, value)} aria-label={`切换 ${task.name}`} /> },
    { title: '执行计划', width: 140, render: (_, task) => task.schedule_type === 'daily' ? `每天 ${task.schedule_time}` : `每 ${task.schedule_minutes} 分钟` },
    { title: '上次运行', dataIndex: 'last_run_at', width: 160, render: formatDate },
    { title: '下次运行', dataIndex: 'next_run_at', width: 160, render: formatDate },
    {
      title: '操作', width: 270,
      render: (_, task) => (
        <div className="row-actions">
          <Button theme="borderless" icon={<IconSearch />} loading={runningAction?.taskId === task.id && runningAction.mode === 'preview'} disabled={Boolean(runningAction)} onClick={() => execute(task, 'preview')}>预览</Button>
          <Button theme="borderless" type="primary" icon={<IconPlay />} loading={runningAction?.taskId === task.id && runningAction.mode === 'submit'} disabled={Boolean(runningAction)} onClick={() => submit(task)}>投递</Button>
          <Button theme="borderless" icon={<IconEdit />} onClick={() => openEdit(task)} aria-label={`编辑 ${task.name}`} />
          <Button theme="borderless" type="danger" icon={<IconDelete />} onClick={() => remove(task)} aria-label={`删除 ${task.name}`} />
        </div>
      ),
    },
  ];
  const previewColumns = [
    { title: '标题', dataIndex: 'title' },
    { title: '发布时间', dataIndex: 'published', width: 160, render: (value) => value || '--' },
    { title: '结果', width: 120, render: (_, item) => item.matched ? (item.magnet_found ? <span className="success-text">可采集</span> : <span className="danger-text">提取失败</span>) : <span className="muted-text">{item.reason}</span> },
  ];

  return (
    <section className={`page tasks-page${embedded ? ' embedded-page' : ''}`}>
      <header className="page-header">
        <div>{embedded ? <h2>RSS 来源</h2> : <h1>RSS 订阅</h1>}<p>配置来源、筛选规则与下载投递目标</p></div>
        <Button theme="solid" type="primary" icon={<IconPlus />} onClick={openNew}>新增订阅</Button>
      </header>
      <div className="toolbar">
        <Input prefix={<IconSearch />} value={query} onChange={setQuery} placeholder="搜索任务名称或 RSS 地址" showClear />
        <Select value={filter} onChange={setFilter} style={{ width: 130 }} optionList={[{ value: 'all', label: '全部状态' }, { value: 'enabled', label: '已启用' }, { value: 'disabled', label: '已停用' }]} />
        <Button icon={<IconRefresh />} onClick={load}>刷新</Button>
      </div>
      <Table rowKey="id" loading={loading} columns={columns} dataSource={visible} pagination={false} empty="暂无 RSS 订阅" />
      <CompletedLibrary />

      <SideSheet
        title={editingId ? '编辑订阅' : '新增订阅'}
        visible={drawer}
        width={560}
        onCancel={() => setDrawer(false)}
        footer={<div className="drawer-footer"><Button onClick={doPreview} loading={previewing}>预览匹配</Button><span /><Button onClick={() => setDrawer(false)}>取消</Button><Button theme="solid" type="primary" loading={saving} onClick={save}>保存</Button></div>}
      >
        <div className="task-form">
          <Field label="任务名称" required hint="便于识别该采集任务"><Input value={form.name} onChange={(value) => set('name', value)} placeholder="例如：高清影片订阅" /></Field>
          <Field label="RSS 地址" required hint="支持 RSS 2.0 与 Atom"><Input value={form.feed_url} onChange={(value) => set('feed_url', value)} placeholder="https://example.com/feed.xml" /></Field>
          <div className="form-grid two">
            <Field label="提取方式"><Select value={form.extractor} onChange={(value) => set('extractor', value)} optionList={[{ value: 'rmdown', label: '详情页两步提取' }, { value: 'direct', label: '直接磁力链接' }]} /></Field>
            <Field label="定时方式"><Select value={form.schedule_type || 'interval'} onChange={(value) => set('schedule_type', value)} optionList={[{ value: 'interval', label: '固定间隔' }, { value: 'daily', label: '每天指定时间' }]} /></Field>
          </div>
          {form.schedule_type === 'daily' ? (
            <Field label="每天执行时间" hint="使用 NAS 系统时区"><Input type="time" value={form.schedule_time || '03:00'} onChange={(value) => set('schedule_time', value)} /></Field>
          ) : (
            <Field label="执行间隔（分钟）" hint="最短 5 分钟，任务完成后重新计算下次执行时间"><InputNumber min={5} max={10080} value={form.schedule_minutes} onChange={(value) => set('schedule_minutes', value)} style={{ width: '100%' }} /></Field>
          )}
          <Field label="必须包含关键词" hint="所有关键词都出现才继续，例如 HD"><Input value={listToText(form.include_all)} onChange={(value) => set('include_all', value)} placeholder="HD" /></Field>
          <Field label="命中任一关键词" hint="使用逗号分隔，命中任意一个即可"><Input value={listToText(form.include_any)} onChange={(value) => set('include_any', value)} placeholder="关键词 A, 关键词 B" /></Field>
          <Field label="排除关键词" hint="命中任意一个就跳过"><Input value={listToText(form.exclude_any)} onChange={(value) => set('exclude_any', value)} placeholder="排除词 A, 排除词 B" /></Field>
          <Field label="任务代理" hint="用于详情页和磁力链接；设置页开启 RSS 代理后也用于订阅源"><Input value={form.proxy_url} onChange={(value) => set('proxy_url', value)} placeholder="http://用户名:密码@主机:端口" /></Field>
          <Field label="下载分类"><Input value={form.qb_category} onChange={(value) => set('qb_category', value)} placeholder="Movies" /></Field>
          <Field label="保存路径" hint="留空时，新发现任务使用设置中的下载来源目录；填写则覆盖默认值。由 qBittorrent 进程负责访问。本版本新入队任务固定保存路径；旧任务仍沿用原订阅/qB 默认规则。"><Input value={form.save_path} onChange={(value) => set('save_path', value)} placeholder="留空使用共享下载目录" /></Field>
          <label className="switch-row"><span><strong>启用任务</strong><small>启用后按计划自动执行</small></span><Switch checked={form.enabled} onChange={(value) => set('enabled', value)} /></label>

          {preview ? (
            <div className="preview-panel">
              <div className="section-heading"><h3>匹配预览（{preview.matched}/{preview.total}）</h3><button type="button" onClick={doPreview}><IconRefresh />重新读取</button></div>
              <Table rowKey={(item, index) => `${item.source_url}-${index}`} size="small" columns={previewColumns} dataSource={preview.entries} pagination={false} />
            </div>
          ) : null}
        </div>
      </SideSheet>
    </section>
  );
}
