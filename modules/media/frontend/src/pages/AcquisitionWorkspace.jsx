import { lazy, Suspense } from 'react';
import WorkspaceTabs from '../components/WorkspaceTabs';

const Tasks = lazy(() => import('./Tasks'));
const Resources = lazy(() => import('./Resources'));
const Wants = lazy(() => import('./Wants'));
const tabs = [{ key: '', label: 'RSS 订阅' }, { key: 'wants', label: '想要清单' }, { key: 'resources', label: '资源搜索' }];

export default function AcquisitionWorkspace({ tab = '', initialCode = '', initial = {}, onTabChange, onNavigate }) {
  return <section className="acquisition-workspace">
    <div className="acquisition-tabs"><WorkspaceTabs label="发现与采集" items={tabs} selected={tab} onChange={onTabChange} /></div>
    <Suspense fallback={<div className="center-state">正在加载采集工具…</div>}>
      {tab === 'resources' ? <Resources initialCode={initialCode} onNavigate={onNavigate} /> : tab === 'wants' ? <Wants key={JSON.stringify(initial)} initial={initial} onNavigate={onNavigate} /> : <Tasks />}
    </Suspense>
  </section>;
}
