import { lazy, Suspense, useEffect, useState } from 'react';
import Spin from '@douyinfe/semi-ui/lib/es/spin';
import Toast from '@douyinfe/semi-ui/lib/es/toast';
import Sidebar from './components/Sidebar';
import Dashboard from './pages/Dashboard';
import { api } from './api';
import { resolveDestination } from './information-architecture';
import { queueJobID } from './queue-view';

const Tasks = lazy(() => import('./pages/Tasks'));
const Catalog = lazy(() => import('./pages/Catalog'));
const People = lazy(() => import('./pages/People'));
const Personal = lazy(() => import('./pages/Personal'));
const Resources = lazy(() => import('./pages/Resources'));
const Queue = lazy(() => import('./pages/Queue'));
const Maintenance = lazy(() => import('./pages/Maintenance'));
const SettingsWorkspace = lazy(() => import('./pages/SettingsWorkspace'));
const resourceKeys = ['actor', 'work', 'list', 'actor_name', 'tag', 'view', 'query', 'library', 'sort', 'offset', 'code'];
const requestedDestination = () => {
  const parameters = new URLSearchParams(window.location.search);
  const result = resolveDestination(parameters.get('page'), parameters.get('tab'));
  const resource = Object.fromEntries(resourceKeys.map(key => [key, (parameters.get(key) || '').slice(0,500)]));
  return { ...result, resource, jobID: result.page === 'queue' ? queueJobID(parameters.get('job')) : null };
};

export default function App() {
  const [destination, setDestination] = useState(requestedDestination);
  const [loading, setLoading] = useState(true);
  const [online, setOnline] = useState(false);
  const [version, setVersion] = useState('');

  useEffect(() => {
    api.health()
      .then((health) => {
        setOnline(Boolean(health.ok));
        setVersion(health.version || '');
      })
      .catch((error) => Toast.error(error.message))
      .finally(() => setLoading(false));
  }, []);

  useEffect(() => {
    const update = () => setDestination(requestedDestination());
    window.addEventListener('popstate', update);
    return () => window.removeEventListener('popstate', update);
  }, []);

  const navigate = (nextPage, tab, jobID, resource = {}) => {
    const next = resolveDestination(nextPage, tab);
    next.jobID = next.page === 'queue' ? queueJobID(jobID) : null;
    const url = new URL(window.location.href);
    url.searchParams.set('page', next.page);
    if (next.tab) url.searchParams.set('tab', next.tab);
    else url.searchParams.delete('tab');
    if (next.jobID) url.searchParams.set('job', String(next.jobID));
    else url.searchParams.delete('job');
    next.resource = resource;
    for (const key of resourceKeys) {
      if (resource[key]) url.searchParams.set(key, String(resource[key]).slice(0,500));
      else url.searchParams.delete(key);
    }
    window.history.pushState(null, '', url);
    setDestination(next);
  };

  const pages = {
    catalog: <Catalog key={JSON.stringify(destination.resource)} initial={destination.resource} onNavigate={navigate} />,
    people: <People actorID={destination.resource?.actor} onNavigate={navigate} />,
    personal: <Personal tab={destination.tab} onTabChange={tab=>navigate('personal',tab)} onNavigate={navigate} />,
    dashboard: <Dashboard onNavigate={navigate} />,
    subscriptions: <Tasks />,
    resources: <Resources initialCode={destination.resource?.code} onNavigate={navigate} />,
    queue: <Queue tab={destination.tab} jobID={destination.jobID} onTabChange={(tab) => navigate('queue', tab)} onNavigate={navigate} />,
    maintenance: <Maintenance />,
    settings: <SettingsWorkspace tab={destination.tab} onTabChange={(tab) => navigate('settings', tab)} onNavigate={navigate} />,
  };

  return (
    <div className="app-shell">
      <Sidebar page={destination.page} onChange={navigate} serviceOnline={online} version={version} />
      <main className="main-content">
        <Suspense fallback={<div className="center-state"><Spin size="large" /></div>}>{loading ? <div className="center-state"><Spin size="large" /></div> : pages[destination.page]}</Suspense>
      </main>
    </div>
  );
}
