import { useEffect, useState } from 'react';
import Button from '@douyinfe/semi-ui/lib/es/button';
import Table from '@douyinfe/semi-ui/lib/es/table';
import Toast from '@douyinfe/semi-ui/lib/es/toast';
import { IconRefresh } from '@douyinfe/semi-icons';
import { api, formatDate, formatDuration } from '../api';
import PipelineStatus from '../components/PipelineStatus';
import { pipelineFailureStages, pipelineTabs } from '../information-architecture';
import Intake from './Intake';
import Javsp from './Javsp';
import Tasks from './Tasks';

const runStatusLabel = { success: '成功', failed: '失败', partial: '部分成功', running: '运行中' };

function PipelineOverview({ data, onOpen }) {
  const runColumns = [
    { title: '时间', dataIndex: 'started_at', width: 150, render: formatDate },
    { title: '模块', dataIndex: 'module_label', width: 120 },
    { title: '任务', dataIndex: 'task_name' },
    { title: '状态', dataIndex: 'status', width: 110, render: (status) => runStatusLabel[status] || status },
    { title: '摘要', dataIndex: 'summary' },
    { title: '耗时', width: 110, render: (_, run) => formatDuration(run.started_at, run.finished_at) },
  ];
  const paths = data?.paths || {};
  return (
    <div className="pipeline-overview-content">
      <section className={`path-diagnostic ${paths.consistent ? 'healthy' : 'attention'}`}>
        <div className="section-heading">
          <div><h2>目录链路</h2><p>下载完成先搬到未处理目录，再刮削整理；开启自动登记后，已确认输出才会进入馆藏。</p></div>
          <span>{paths.consistent ? '配置连贯' : `${paths.issues?.length || 0} 项需处理`}</span>
        </div>
        <div className="path-chain">
          <div><small>qB 下载目录</small><code>{paths.rss_download_paths?.join('、') || '使用 qB 默认目录'}</code></div>
          <i>→</i>
          <div><small>入库来源</small><code>{paths.intake_source}</code></div>
          <i>→</i>
          <div><small>JAVSP 输入</small><code>{paths.javsp_input}</code></div>
          <i>→</i>
          <div><small>成品媒体库</small><code>{paths.completed_directory || '原地整理或尚未设置'}</code><small>目录模板：{paths.javsp_output_pattern || '--'}</small></div>
        </div>
        {(paths.issues || []).map((issue) => <div className="pipeline-issue" key={issue}>{issue}</div>)}
        {(paths.warnings || []).map((warning) => <div className="pipeline-warning" key={warning}>{warning}</div>)}
      </section>

      <section className="pipeline-actions-panel">
        <div className="section-heading"><div><h2>阶段操作</h2><p>每个动作只控制一个阶段，避免误触整条流水线。</p></div></div>
        <div className="pipeline-action-grid">
          <button type="button" onClick={() => onOpen('rss')}><strong>管理 RSS 来源</strong><span>预览、投递和调整筛选规则</span></button>
          <button type="button" onClick={() => onOpen('intake')}><strong>检查完成下载</strong><span>查看候选并执行入库</span></button>
          <button type="button" onClick={() => onOpen('javsp')}><strong>启动 JAVSP</strong><span>检查组件、整理影片和查看输出</span></button>
        </div>
      </section>

      <section className="pipeline-recent-runs">
        <div className="section-heading"><h2>流水线最近运行</h2><span className="muted-text">跨模块统一展示</span></div>
        <Table rowKey="id" columns={runColumns} dataSource={data?.recent_runs || []} pagination={false} empty="暂无流水线运行记录" />
      </section>
    </div>
  );
}

export default function Pipeline({ initialTab, onTabChange, onNavigate }) {
  const validInitial = pipelineTabs.some((item) => item.key === initialTab) ? initialTab : 'overview';
  const [tab, setTab] = useState(validInitial);
  const [data, setData] = useState(null);
  const [loading, setLoading] = useState(false);
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
  useEffect(() => {
    if (pipelineTabs.some((item) => item.key === initialTab)) setTab(initialTab);
  }, [initialTab]);

  const selectTab = (nextTab) => {
    if (nextTab === 'catalog') { onNavigate?.('queue', 'jobs'); return; }
    setTab(nextTab);
    onTabChange?.(nextTab);
  };

  const handleTabKeyDown = (event, index) => {
    let nextIndex = index;
    if (event.key === 'ArrowRight') nextIndex = (index + 1) % pipelineTabs.length;
    else if (event.key === 'ArrowLeft') nextIndex = (index - 1 + pipelineTabs.length) % pipelineTabs.length;
    else if (event.key === 'Home') nextIndex = 0;
    else if (event.key === 'End') nextIndex = pipelineTabs.length - 1;
    else return;
    event.preventDefault();
    const nextTab = pipelineTabs[nextIndex].key;
    selectTab(nextTab);
    document.getElementById(`pipeline-tab-${nextTab}`)?.focus();
  };

  return (
    <section className="page pipeline-page">
      <header className="page-header">
        <div><h1>采集流水线</h1><p>从 RSS 发现、下载完成到 JAVSP 整理，在一个工作区完成。</p></div>
        <Button icon={<IconRefresh />} loading={loading} onClick={load}>刷新状态</Button>
      </header>

      <PipelineStatus stages={loadError ? pipelineFailureStages(loadError) : data?.stages} activeTab={tab} onSelect={selectTab} />

      <div className="workspace-tabs" role="tablist" aria-label="采集流水线功能">
        {pipelineTabs.map((item, index) => <button type="button" role="tab" id={`pipeline-tab-${item.key}`} aria-controls="pipeline-tabpanel" aria-selected={tab === item.key} tabIndex={tab === item.key ? 0 : -1} className={tab === item.key ? 'selected' : ''} key={item.key} onClick={() => selectTab(item.key)} onKeyDown={(event) => handleTabKeyDown(event, index)}>{item.label}</button>)}
      </div>

      <div className="pipeline-panel" id="pipeline-tabpanel" role="tabpanel" aria-labelledby={`pipeline-tab-${tab}`} tabIndex={0}>
        {loadError ? <div className="workspace-error" role="alert"><div><strong>无法读取流水线状态</strong><span>{loadError}</span></div><Button onClick={load}>重新读取</Button></div> : null}
        {tab === 'overview' && data ? <PipelineOverview data={data} onOpen={selectTab} /> : null}
        {tab === 'rss' ? <Tasks embedded /> : null}
        {tab === 'intake' ? <Intake embedded /> : null}
        {tab === 'javsp' ? <Javsp embedded onNavigate={onNavigate} /> : null}
      </div>
    </section>
  );
}
