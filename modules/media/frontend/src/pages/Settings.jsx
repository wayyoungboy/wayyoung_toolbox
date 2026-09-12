import { useEffect, useState } from 'react';
import Button from '@douyinfe/semi-ui/lib/es/button';
import Input from '@douyinfe/semi-ui/lib/es/input';
import InputNumber from '@douyinfe/semi-ui/lib/es/inputNumber';
import Switch from '@douyinfe/semi-ui/lib/es/switch';
import Toast from '@douyinfe/semi-ui/lib/es/toast';
import { IconLink, IconSave } from '@douyinfe/semi-icons';
import { api } from '../api';
import { settingsPayload } from '../settingsPayload';

function Field({ label, hint, children }) {
  return <label className="settings-field"><span>{label}</span>{children}{hint ? <small>{hint}</small> : null}</label>;
}

export default function Settings({ embedded = false }) {
  const [form, setForm] = useState(null);
  const [passwordChanged, setPasswordChanged] = useState(false);
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const set = (key, value) => setForm((current) => ({ ...current, [key]: value }));

  useEffect(() => {
    api.settings().then((value) => setForm({ ...value, qb_password: '' })).catch((error) => Toast.error(error.message));
  }, []);

  if (!form) return <div className="center-state">正在读取设置…</div>;

  const candidate = () => settingsPayload(form, passwordChanged);
  const save = async () => {
    setSaving(true);
    try {
      const value = await api.updateSettings(candidate());
      setForm({ ...value, qb_password: '' });
      setPasswordChanged(false);
      Toast.success('设置已保存');
    } catch (error) { Toast.error(error.message); }
    finally { setSaving(false); }
  };
  const test = async () => {
    setTesting(true);
    try {
      const result = await api.testQbittorrent(candidate());
      Toast.success(`连接成功，qBittorrent ${result.version}`);
    } catch (error) { Toast.error(error.message); }
    finally { setTesting(false); }
  };

  return (
    <section className={`page settings-page${embedded ? ' embedded-page' : ''}`}>
      <header className="page-header"><div>{embedded ? <h2>网络与服务</h2> : <h1>设置</h1>}<p>配置下载服务、网络代理与安全运行模式</p></div><Button theme="solid" type="primary" icon={<IconSave />} loading={saving} onClick={save}>保存设置</Button></header>
      <div className="settings-layout">
        <section className="settings-section">
          <div className="settings-title"><h2>安全模式</h2><p>首次配置建议保持预览模式，确认规则后再实际投递。</p></div>
          <label className="switch-row setting-switch"><span><strong>预览模式</strong><small>任务会解析和匹配条目，但不会发送给 qBittorrent</small></span><Switch checked={form.preview_mode} onChange={(value) => set('preview_mode', value)} /></label>
        </section>
        <section className="settings-section">
          <div className="settings-title"><h2>qBittorrent Web UI</h2><p>凭据保存在应用私有数据目录，不会返回给浏览器或写入日志。</p></div>
          <div className="settings-grid">
            <Field label="服务地址" hint="必须包含 http:// 或 https://"><Input value={form.qb_host} onChange={(value) => set('qb_host', value)} placeholder="http://NAS 地址" /></Field>
            <Field label="端口"><InputNumber min={1} max={65535} value={form.qb_port} onChange={(value) => set('qb_port', value)} style={{ width: '100%' }} /></Field>
            <Field label="用户名"><Input value={form.qb_username} onChange={(value) => set('qb_username', value)} autoComplete="off" /></Field>
            <Field label="密码" hint={form.qb_password_set && !passwordChanged ? '已保存；留空表示不修改' : '密码不会显示在界面中'}><Input mode="password" value={form.qb_password} onChange={(value) => { set('qb_password', value); setPasswordChanged(true); }} autoComplete="new-password" placeholder={form.qb_password_set ? '••••••••' : '输入密码'} /></Field>
          </div>
          <div className="inline-actions"><Button icon={<IconLink />} loading={testing} onClick={test}>测试连接</Button><label className="inline-switch"><Switch size="small" checked={form.qb_verify_certificate} onChange={(value) => set('qb_verify_certificate', value)} />验证 HTTPS 证书</label></div>
        </section>
        <section className="settings-section">
          <div className="settings-title"><h2>网络与运行记录</h2><p>详情页和磁力解析可使用代理；RSS 可选代理；qBittorrent 始终直连。</p></div>
          <div className="settings-grid">
            <Field label="全局解析代理" hint="用于详情页和磁力链接；开启 RSS 代理后也作为订阅源的后备代理"><Input value={form.global_proxy_url} onChange={(value) => set('global_proxy_url', value)} placeholder="http://用户名:密码@主机:端口" /></Field>
            <Field label="请求超时（秒）"><InputNumber min={5} max={120} value={form.request_timeout} onChange={(value) => set('request_timeout', value)} style={{ width: '100%' }} /></Field>
            <Field label="RSS 运行记录保留数量" hint="保存后立即删除超过数量的旧记录"><InputNumber min={1} max={100} value={form.run_retention_count} onChange={(value) => set('run_retention_count', value)} style={{ width: '100%' }} /></Field>
          </div>
          <label className="switch-row setting-switch"><span><strong>RSS 订阅源使用代理</strong><small>开启后优先使用任务代理，否则使用全局解析代理</small></span><Switch checked={form.feed_use_proxy} onChange={(value) => set('feed_use_proxy', value)} /></label>
        </section>
      </div>
    </section>
  );
}
