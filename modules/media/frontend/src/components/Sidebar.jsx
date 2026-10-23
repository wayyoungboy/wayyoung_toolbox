import {
  IconHome,
  IconSignal,
  IconList,
  IconSearch,
  IconSetting,
} from '@douyinfe/semi-icons';
import { primaryNavigation } from '../information-architecture';

const icons = {
  catalog: IconHome,
  dashboard: IconHome,
  subscriptions: IconSignal,
  queue: IconList,
  maintenance: IconSearch,
  settings: IconSetting,
};

export default function Sidebar({ page, onChange, serviceOnline, version }) {
  return (
    <aside className="sidebar">
      <div className="brand">
        <img src={`${import.meta.env.BASE_URL}icon_64.png`} alt="" />
        <span>个人媒体中心</span>
      </div>
      <nav className="nav-list" aria-label="应用导航">
        {primaryNavigation.map((item, index) => {
          const Icon = icons[item.key];
          const separated = index > 0 && primaryNavigation[index - 1].group !== item.group;
          return [
            separated ? <div className="nav-separator" key={`${item.key}-separator`} /> : null,
            <button
              type="button"
              className={`nav-item ${page === item.key ? 'selected' : ''}`}
              aria-current={page === item.key ? 'page' : undefined}
              aria-label={item.label}
              onClick={() => onChange(item.key)}
              key={item.key}
            >
              <Icon size="large" aria-hidden />
              <span>{item.label}</span>
            </button>,
          ];
        })}
      </nav>
      <div className="sidebar-footer">
        <div className="service-line">
          <span className={`status-dot ${serviceOnline ? 'success' : 'danger'}`} />
          {serviceOnline ? '服务运行中' : '服务不可用'}
        </div>
        <span>版本 {version || '--'}</span>
      </div>
    </aside>
  );
}
