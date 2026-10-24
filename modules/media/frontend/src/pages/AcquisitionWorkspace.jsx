import { lazy, Suspense } from 'react';
import WorkspaceTabs from '../components/WorkspaceTabs';

const Tasks = lazy(() => import('./Tasks'));
const Resources = lazy(() => import('./Resources'));
const tabs = [{ key: '', label: 'RSS 订阅' }, { key: 'resources', label: '资源搜索' }];

export default function AcquisitionWorkspace({ tab = '', initialCode = '', onTabChange, onNavigate }) {
  return <section className="acquisition-workspace">
    <div className="acquisition-tabs"><WorkspaceTabs label="发现与采集" items={tabs} selected={tab} onChange={onTabChange} /></div>
    <Suspense fallback={<div className="center-state">正在加载采集工具…</div>}>
      {tab === 'resources' ? <Resources initialCode={initialCode} onNavigate={onNavigate} /> : <Tasks />}
    </Suspense>
  </section>;
}
