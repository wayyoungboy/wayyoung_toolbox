import { useEffect, useMemo, useState } from 'react';
import Button from '@douyinfe/semi-ui/lib/es/button';
import Table from '@douyinfe/semi-ui/lib/es/table';
import Toast from '@douyinfe/semi-ui/lib/es/toast';
import { IconRefresh, IconSignal } from '@douyinfe/semi-icons';
import { api, formatDate, formatDuration } from '../api';
import PipelineStatus from '../components/PipelineStatus';
import QueueWorkbench from '../components/QueueWorkbench';
import { QueueStatus } from '../components/QueueViews';


export default function Dashboard({ onNavigate }) {
  const [data, setData] = useState(null);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState('');

  const load = async () => {
    setLoading(true);
    try {
      setData(await api.pipeline());
      setLoadError('');
    } catch (error) {
      setLoadError(error.message);
      Toast.error(error.message);
    }
    finally { setLoading(false); }
  };

  useEffect(() => { load(); }, []);

  const attention = useMemo(() => {
    const stages = (data?.stages || [])
      .filter((stage) => ['warning', 'error'].includes(stage.status))
      .map((stage) => ({ id: `stage-${stage.key}`, title: stage.label, message: stage.error || stage.detail }));
    const service = loadError ? [{ id: 'workspace-load', title: '工作台状态读取失败', message: loadError }] : [];
    return [...service, ...stages].slice(0, 5);
  }, [data, loadError]);

  const runColumns = [
    { title: '时间', dataIndex: 'started_at', width: 150, render: formatDate },
    { title: '模块', dataIndex: 'module_label', width: 120 },
    { title: '任务', dataIndex: 'task_name' },
    { title: '状态', dataIndex: 'status', width: 110, render: (status) => <QueueStatus status={status} /> },
    { title: '摘要', dataIndex: 'summary' },
    { title: '耗时', width: 105, render: (_, run) => formatDuration(run.started_at, run.finished_at) },
  ];

  return (
    <section className="page dashboard-page">
      <header className="page-header">
        <div><h1>工作台</h1><p>查看从订阅发现到影片整理的实时状态和待处理事项。</p></div>
        <div className="header-actions">
          <Button icon={<IconRefresh />} loading={loading} onClick={load}>刷新状态</Button>
          <Button theme="solid" type="primary" icon={<IconSignal />} onClick={() => onNavigate('queue')}>进入处理队列</Button>
        </div>
      </header>

      <QueueWorkbench onNavigate={onNavigate} />
      <div className="section-heading"><h2>服务与目录状态</h2></div>
      <PipelineStatus stages={data?.stages} onSelect={(tab) => onNavigate('pipeline', tab)} />

      <div className="workbench-grid">
        <section className={`workbench-card path-card ${data?.paths?.consistent ? 'healthy' : 'attention'}`}>
          <div className="section-heading">
            <div><h2>目录链路</h2><p>确认下载、入库与 JAVSP 扫描目录能连续流转。</p></div>
            <span>{loadError ? '状态未知' : data?.paths?.consistent ? '配置连贯' : '需要检查'}</span>
          </div>
          <div className="compact-path-chain">
            <div><small>下载</small><code>{data?.paths?.rss_download_paths?.join('、') || 'qB 默认目录'}</code></div>
            <i>→</i>
            <div><small>入库</small><code>{data?.paths?.intake_source || '--'}</code></div>
            <i>→</i>
            <div><small>整理</small><code>{data?.paths?.javsp_input || '--'}</code></div>
          </div>
          <button type="button" className="text-action" onClick={() => onNavigate('pipeline')}>查看完整链路与阶段操作</button>
        </section>

        <section className="workbench-card attention-card">
          <div className="section-heading"><div><h2>服务提醒</h2><p>连接、组件和目录配置的检查结果。</p></div><span>{loading ? '读取中' : `${attention.length} 项`}</span></div>
          {attention.length ? (
            <ul className="attention-list">
              {attention.map((item) => <li key={item.id}><strong>{item.title}</strong><span>{item.message}</span></li>)}
            </ul>
          ) : loading ? <p className="queue-footnote">正在读取服务状态…</p> : <div className="healthy-empty"><span className="status-dot success" />当前未发现服务提醒</div>}
        </section>
      </div>

      <section className="workbench-runs">
        <div className="section-heading"><div><h2>最近运行</h2><p>RSS、下载入库、JAVSP 和媒体维护统一展示。</p></div><button type="button" onClick={() => onNavigate('runs')}>查看全部</button></div>
        <Table rowKey="id" columns={runColumns} dataSource={data?.recent_runs || []} pagination={false} empty="暂无运行记录" />
      </section>
    </section>
  );
}
