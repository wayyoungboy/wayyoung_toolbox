import { useCallback, useEffect, useRef, useState } from 'react';
import Button from '@douyinfe/semi-ui/lib/es/button';
import Select from '@douyinfe/semi-ui/lib/es/select';
import Table from '@douyinfe/semi-ui/lib/es/table';
import Toast from '@douyinfe/semi-ui/lib/es/toast';
import { IconRefresh } from '@douyinfe/semi-icons';
import { api, formatDate, formatDuration } from '../api';
import { runModuleOptions, unifiedRunModeLabel } from '../information-architecture';
import { queueStatuses } from '../queue-view';
import { QueueStatus } from '../components/QueueViews';

const statusOptions = [
  { value: '', label: '全部状态' },
  ...queueStatuses.map(([value, label]) => ({ value, label })),
];

const triggerOptions = [
  { value: '', label: '全部触发方式' },
  { value: 'manual', label: '手动' },
  { value: 'schedule', label: '计划' },
  { value: 'queue', label: '队列' },
  { value: 'manual_scan', label: '手动扫描' },
  { value: 'schedule_scan', label: '计划扫描' },
];

const triggerLabels = Object.fromEntries(triggerOptions.map(({ value, label }) => [value, label]));

function RunDetails({ run }) {
  const details = run.details;
  const items = Array.isArray(details) ? details : [];
  const text = typeof details === 'string' ? details : '';
  return (
    <div className="run-detail">
      {run.error ? <div className="error-box">{run.error}</div> : null}
      {items.length ? (
        <ul>{items.map((item, index) => {
          if (typeof item === 'string') return <li key={`${item}-${index}`}><span>{item}</span></li>;
          return <li key={`${item?.title || item?.message || 'detail'}-${index}`}><span className={`detail-status ${item?.status || ''}`}>{item?.status || '信息'}</span><strong>{item?.title}</strong>{item?.message ? <span>{item.message}</span> : null}</li>;
        })}</ul>
      ) : text ? <pre className="unified-run-output">{text}</pre> : !run.error ? <span className="muted-text">没有更多运行明细</span> : null}
    </div>
  );
}

export default function Runs({ embedded = false }) {
  const [runs, setRuns] = useState([]);
  const [loading, setLoading] = useState(true);
  const [filters, setFilters] = useState({ module: '', status: '', trigger: '' });
  const requestID = useRef(0);

  const load = useCallback(async () => {
    const currentRequest = ++requestID.current;
    setLoading(true);
    setRuns([]);
    try {
      const result = await api.unifiedRuns({ ...filters, limit: 100 });
      if (currentRequest === requestID.current) setRuns(result.items || []);
    } catch (error) {
      if (currentRequest === requestID.current) Toast.error(error.message);
    } finally {
      if (currentRequest === requestID.current) setLoading(false);
    }
  }, [filters]);

  useEffect(() => { load(); }, [load]);

  const columns = [
    { title: '开始时间', dataIndex: 'started_at', width: 160, render: formatDate },
    { title: '模块', dataIndex: 'module_label', width: 120 },
    { title: '任务', dataIndex: 'task_name', width: 180 },
    { title: '触发方式', dataIndex: 'trigger_type', width: 100, render: (value) => triggerLabels[value] || value || '--' },
    { title: '模式', width: 80, render: (_, run) => unifiedRunModeLabel(run) },
    { title: '状态', dataIndex: 'status', width: 110, render: (status) => <QueueStatus status={status} /> },
    { title: '结果摘要', dataIndex: 'summary' },
    { title: '运行耗时', width: 105, render: (_, run) => formatDuration(run.started_at, run.finished_at) },
  ];

  const setFilter = (key, value) => setFilters((current) => ({ ...current, [key]: value || '' }));

  return (
    <section className={`page runs-page${embedded ? ' embedded-page' : ''}`}>
      <header className="page-header">
        <div>{embedded ? <h2>运行记录</h2> : <h1>运行记录</h1>}<p>统一查看订阅、下载入库、影片整理和媒体维护的执行结果。</p></div>
        <Button icon={<IconRefresh />} loading={loading} onClick={load}>刷新</Button>
      </header>
      <div className="run-filters" aria-label="运行记录筛选">
        <Select value={filters.module} optionList={runModuleOptions} onChange={(value) => setFilter('module', value)} />
        <Select value={filters.status} optionList={statusOptions} onChange={(value) => setFilter('status', value)} />
        <Select value={filters.trigger} optionList={triggerOptions} onChange={(value) => setFilter('trigger', value)} />
        <span>{runs.length} 条记录</span>
      </div>
      <Table
        rowKey="id"
        loading={loading}
        columns={columns}
        dataSource={runs}
        pagination={{ pageSize: 20 }}
        empty="当前筛选条件下没有运行记录"
        expandedRowRender={(run) => <RunDetails run={run} />}
      />
    </section>
  );
}
