import { pipelineStageTab } from '../information-architecture';

const statusLabel = {
  ok: '正常',
  idle: '等待中',
  warning: '需检查',
  error: '异常',
  running: '运行中',
};

const fallbackStages = [
  { key: 'rss', label: 'RSS 发现', status: 'idle', summary: '读取中', detail: '' },
  { key: 'qb', label: 'qBittorrent 下载', status: 'idle', summary: '读取中', detail: '' },
  { key: 'intake', label: '完成入库', status: 'idle', summary: '读取中', detail: '' },
  { key: 'javsp', label: 'JAVSP 整理', status: 'idle', summary: '读取中', detail: '' },
];

export default function PipelineStatus({ stages, activeTab, onSelect }) {
  return (
    <div className="pipeline-ribbon" aria-label="采集流水线状态">
      {(stages?.length ? stages : fallbackStages).map((stage, index) => {
        const targetTab = pipelineStageTab(stage.key);
        return (
          <button
            type="button"
            className={`pipeline-stage ${stage.status} ${activeTab === targetTab ? 'active' : ''}`}
            onClick={() => onSelect?.(targetTab)}
            key={stage.key}
          >
            <span className="pipeline-stage-index">{String(index + 1).padStart(2, '0')}</span>
            <span className="pipeline-stage-copy">
              <strong>{stage.label}</strong>
              <span>{stage.summary}</span>
              <small>{stage.error || stage.detail}</small>
            </span>
            <span className={`pipeline-stage-state ${stage.status}`}>{statusLabel[stage.status] || stage.status}</span>
          </button>
        );
      })}
    </div>
  );
}
