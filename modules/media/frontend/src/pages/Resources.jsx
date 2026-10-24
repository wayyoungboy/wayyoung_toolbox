import ResourceSearch from '../components/ResourceSearch';
import '../catalog.css';

export default function Resources({ initialCode = '', onNavigate }) {
  return <div className="page resource-page">
    <header className="page-header"><div><h1>资源搜索</h1><p>按完整番号查询候选，确认资料后自行选择；搜索与下载分开。</p></div></header>
    <div className="resource-panel"><ResourceSearch key={initialCode} initialCode={initialCode} /></div>
    <p className="catalog-help">目前接入 JavDB，其他来源与云下载尚未接入此入口。<button type="button" className="catalog-text-button" onClick={() => onNavigate('settings', 'javsp')}>检查 JAVSP 站点与网络配置</button></p>
  </div>;
}
