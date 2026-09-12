import { lazy, Suspense } from 'react';
import WorkspaceTabs from '../components/WorkspaceTabs';
import { settingsTabs } from '../information-architecture';
import Settings from './Settings';

const Intake = lazy(() => import('./Intake'));
const Javsp = lazy(() => import('./Javsp'));
const Anime = lazy(() => import('./Anime'));
const Pipeline = lazy(() => import('./Pipeline'));
const MediaPaths = lazy(() => import('./MediaPaths'));

export default function SettingsWorkspace({ tab = 'paths', onTabChange, onNavigate }) {
  return <section className="page settings-workspace"><header className="page-header"><div><h1>设置</h1><p>集中管理连接、入库、JAVSP、翻译和自动执行；现有高级配置与番剧工具保留。</p></div></header>
    <WorkspaceTabs label="设置分类" items={settingsTabs} selected={tab} onChange={onTabChange} />
    <div className="settings-workspace-body"><Suspense fallback={<div className="center-state">正在加载设置…</div>}>
      {tab === 'paths' ? <MediaPaths /> : null}
      {tab === 'general' ? <Settings embedded /> : null}
      {tab === 'intake' ? <Intake embedded /> : null}
      {tab === 'javsp' ? <Javsp embedded onNavigate={onNavigate} /> : null}
      {tab === 'diagnostics' ? <Pipeline initialTab="overview" onNavigate={onNavigate} /> : null}
      {tab === 'anime' ? <Anime /> : null}
    </Suspense></div>
  </section>;
}
