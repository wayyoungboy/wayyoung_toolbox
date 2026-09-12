import { useCallback, useEffect, useRef, useState } from 'react';
import Button from '@douyinfe/semi-ui/lib/es/button';
import Input from '@douyinfe/semi-ui/lib/es/input';
import InputNumber from '@douyinfe/semi-ui/lib/es/inputNumber';
import Modal from '@douyinfe/semi-ui/lib/es/modal';
import Select from '@douyinfe/semi-ui/lib/es/select';
import Switch from '@douyinfe/semi-ui/lib/es/switch';
import Table from '@douyinfe/semi-ui/lib/es/table';
import TagInput from '@douyinfe/semi-ui/lib/es/tagInput';
import Toast from '@douyinfe/semi-ui/lib/es/toast';
import { IconDelete, IconFolderOpen, IconPlus, IconPlay, IconRefresh, IconSave, IconScan } from '@douyinfe/semi-icons';
import { api, formatDate, formatDuration } from '../api';
import FolderPicker from '../components/FolderPicker';
import JavspNamingPreview from '../components/JavspNamingPreview';
import { javspPayload, translatorNeedsAPIKey, translatorOptions } from '../javsp-config';
import { javspProgressModel } from '../javsp-progress';
import { javspActivity, javspRunOutput, javspTriggerLabel } from '../javsp-activity';
import { QueueStatus } from '../components/QueueViews';
import { queueStatusLabel } from '../queue-view';
import { useRemoteResource } from '../useRemoteResource';

const outputDirectories = (path) => api.javspDirectories(path, { target: 'output' });

function Field({ label, hint, wide, children }) {
  return <label className={`settings-field${wide ? ' full-field' : ''}`}><span>{label}</span>{children}{hint ? <small>{hint}</small> : null}</label>;
}

function Summary({ label, value, hint, tone }) {
  return <div className="stat-block"><div className={`stat-value ${tone || ''}`}>{value}</div><div className="stat-label">{label}</div>{hint ? <div className="stat-hint">{hint}</div> : null}</div>;
}

function ListField({ label, hint, value, onChange, wide = true }) {
  return (
    <Field label={label} hint={hint || '输入内容后按 Enter 添加；可拖动查看完整内容'} wide={wide}>
      <TagInput value={value || []} onChange={onChange} addOnBlur showClear style={{ width: '100%' }} />
    </Field>
  );
}

function Toggle({ label, hint, checked, onChange }) {
  return <label className="switch-row"><span><strong>{label}</strong><small>{hint}</small></span><Switch checked={checked} onChange={onChange} /></label>;
}

function ProxyFreeEditor({ value, onChange }) {
  const entries = Object.entries(value || {});
  const update = (index, nextKey, nextValue) => {
    const next = {};
    entries.forEach(([key, address], itemIndex) => {
      next[itemIndex === index ? nextKey : key] = itemIndex === index ? nextValue : address;
    });
    onChange(next);
  };
  const add = () => {
    let index = entries.length + 1;
    let key = `site_${index}`;
    while (Object.hasOwn(value || {}, key)) { index += 1; key = `site_${index}`; }
    onChange({ ...(value || {}), [key]: 'https://' });
  };
  const remove = (target) => onChange(Object.fromEntries(entries.filter(([key]) => key !== target)));
  return (
    <div className="settings-field full-field proxy-free-editor">
      <span>站点免代理入口</span>
      <div className="map-editor">
        {entries.map(([key, address], index) => (
          <div className="map-editor-row" key={index}>
            <Input aria-label={`免代理站点 ${index + 1}`} value={key} onChange={(nextKey) => update(index, nextKey, address)} placeholder="爬虫 ID" />
            <Input aria-label={`免代理地址 ${index + 1}`} value={address} onChange={(nextAddress) => update(index, key, nextAddress)} placeholder="https://" />
            <Button aria-label={`删除免代理站点 ${key}`} icon={<IconDelete />} type="danger" onClick={() => remove(key)} />
          </div>
        ))}
      </div>
      <Button className="map-add-button" icon={<IconPlus />} onClick={add}>添加站点</Button>
      <small>按爬虫 ID 覆盖入口地址；这些地址不经过 JAVSP 代理</small>
    </div>
  );
}

export default function Javsp({ embedded = false, onNavigate }) {
  const read = useCallback((signal) => api.javsp({ signal }), []);
  const resource = useRemoteResource(read, 2500);
  const { data, refresh } = resource;
  const [form, setForm] = useState(null);
  const [saving, setSaving] = useState(false);
  const [configConflict, setConfigConflict] = useState(false);
  const [reloadingConfig, setReloadingConfig] = useState(false);
  const configReload = useRef(null);
  const [testing, setTesting] = useState(false);
  const [starting, setStarting] = useState(false);
  const [cancelingScan, setCancelingScan] = useState(false);
  const [confirmation, setConfirmation] = useState(null);
  const [folderPickerTarget, setFolderPickerTarget] = useState(null);

  useEffect(() => {
    if (data?.config) setForm((current) => current || data.config);
  }, [data?.config]);
  useEffect(() => () => {
    configReload.current?.abort();
    configReload.current = null;
  }, []);

  if (resource.error) return <div className="center-state" role="alert"><p>{resource.error}</p><Button onClick={refresh}>重新读取</Button></div>;
  if (!data || !form) return <div className="center-state">正在读取 JAVSP 配置…</div>;
  const activity = javspActivity(data);
  const confirming = starting || cancelingScan || reloadingConfig;
  const set = (key, value) => setForm((current) => ({ ...current, [key]: value }));
  const saveError = (error) => {
    if (error.status === 409) setConfigConflict(true);
    Toast.error(error.message);
  };

  const reloadConfig = () => setConfirmation({
    title: '读取最新配置？',
    content: '这会替换本页未保存的修改，不会启动扫描或影响正在执行的影片。',
    okText: '确认读取',
    okButtonProps: { 'aria-label': '确认读取' },
    onOk: async () => {
      if (configReload.current) return;
      const controller = new AbortController();
      configReload.current = controller;
      setReloadingConfig(true);
      try {
        const latest = await api.javsp({ signal: controller.signal });
        if (controller.signal.aborted || configReload.current !== controller) return;
        setForm(latest.config);
        setConfigConflict(false);
        refresh();
      } catch (error) {
        if (!controller.signal.aborted) Toast.error(error.message);
      } finally {
        if (configReload.current === controller) {
          configReload.current = null;
          setReloadingConfig(false);
        }
      }
    },
  });

  const save = async () => {
    setSaving(true);
    try {
      const config = await api.updateJavsp(javspPayload(form));
      setForm(config);
      setConfigConflict(false);
      refresh();
      Toast.success('JAVSP 配置已保存');
    } catch (error) { saveError(error); }
    finally { setSaving(false); }
  };

  const test = async () => {
    setTesting(true);
    try {
      const result = await api.testJavsp();
      Toast.success(`组件可用：${result.version}`);
    } catch (error) { Toast.error(error.message); }
    finally { setTesting(false); }
  };

  const run = () => setConfirmation({
    title: '扫描文件并加入处理队列？',
    content: `先保存当前配置，再扫描“${form.input_directory}”。扫描本身不移动文件；发现的影片入队后会自动${form.move_files ? '按当前配置生成元数据并整理文件' : '生成元数据，不移动影片文件'}。已有整理任务会继续运行。`,
    okText: '开始扫描',
    okButtonProps: { 'aria-label': '开始扫描' },
    onOk: async () => {
      setStarting(true);
      try {
        const config = await api.updateJavsp(javspPayload(form));
        setForm(config);
        setConfigConflict(false);
        await api.runJavsp();
        Toast.success('文件发现已启动，实际整理进度请查看处理队列');
      } catch (error) { saveError(error); }
      finally { refresh(); setStarting(false); }
    },
  });

  const cancelScan = () => {
    const runID = activity.cancelScanRunID;
    if (!runID) return;
    setConfirmation({
      title: `取消文件发现 #${runID}？`,
      content: '只停止本次目录扫描，不会取消已入队影片或当前正在整理的影片。',
      okText: '确认取消扫描',
      okButtonProps: { 'aria-label': '确认取消扫描' },
      okType: 'danger',
      onOk: async () => {
        setCancelingScan(true);
        try {
          await api.cancelJavspDiscovery(runID);
          Toast.success('已发送扫描取消请求');
        } catch (error) { Toast.error(error.message); }
        finally { refresh(); setCancelingScan(false); }
      },
    });
  };

  const latest = data.runs?.[0];
  const progress = javspProgressModel(data.progress);
  const columns = [
    { title: '开始时间', dataIndex: 'started_at', width: 170, render: formatDate },
    { title: '触发方式', dataIndex: 'trigger_type', width: 100, render: javspTriggerLabel },
    { title: '状态', dataIndex: 'status', width: 120, render: (status) => <QueueStatus status={status} /> },
    { title: '退出码', dataIndex: 'exit_code', width: 90, render: (value) => value ?? '--' },
    { title: '运行耗时', width: 120, render: (_, item) => formatDuration(item.started_at, item.finished_at) },
  ];

  return (
    <section className={`page javsp-page${embedded ? ' embedded-page' : ''}`}>
      <header className="page-header">
        <div>{embedded ? <h2>JAVSP 整理</h2> : <h1>JAVSP 整理</h1>}<p>扫描发现后按影片入队；逐部生成元数据并整理，进度在处理队列中查看</p></div>
        <div className="header-actions">
          <Button icon={<IconScan />} loading={testing} onClick={test}>检测组件</Button>
          <Button icon={<IconRefresh />} aria-label="刷新" onClick={refresh}>刷新</Button>
          {activity.organizing ? <Button onClick={() => onNavigate('queue', 'jobs')}>查看整理队列</Button> : null}
          {activity.scanning ? <Button type="danger" loading={cancelingScan} disabled={!activity.cancelScanRunID} onClick={cancelScan}>取消扫描</Button> : null}
          <Button theme="solid" type="primary" icon={<IconPlay aria-hidden="true" />} aria-label={activity.scanning ? '扫描中' : '扫描并加入队列'} loading={starting} disabled={starting || !activity.canScan} onClick={run}>{activity.scanning ? '扫描中' : '扫描并加入队列'}</Button>
        </div>
      </header>

      {configConflict ? <section className="notice-box" role="alert" aria-label="配置保存冲突">
        <strong>配置已更新，本页修改尚未保存</strong>
        <span>另一个页面已保存新配置。未覆盖新配置，也未因本次保存启动扫描；可以先保留或记录草稿，再读取最新配置。</span>
        <Button htmlType="button" disabled={saving || confirming} onClick={reloadConfig}>读取最新配置</Button>
      </section> : null}

      <div className="overview-strip javsp-overview">
        <Summary label="运行组件" value={data.component.available ? '可用' : '不可用'} hint={data.component.version} tone={data.component.available ? 'success-text' : 'danger-text'} />
        <Summary label="自动执行" value={form.enabled ? (form.schedule_type === 'daily' ? `每天 ${form.schedule_time}` : `每 ${form.schedule_minutes} 分钟`) : '已关闭'} hint="默认关闭，保存后生效" />
        <Summary label="下次运行" value={formatDate(data.config.next_run_at)} hint="使用 NAS 系统时区" />
        <Summary label="上次运行" value={formatDate(data.config.last_run_at)} hint={latest ? queueStatusLabel(latest.status) : '暂无运行记录'} />
      </div>

      {data.discovery?.run_id > 0 ? <section className="notice-box" aria-label="文件发现状态" aria-live="polite">
        <strong>文件发现 #{data.discovery.run_id} · <QueueStatus status={data.discovery.status} /></strong>
        <span>{activity.scanning ? '正在扫描目录并准备入队，不代表影片正在刮削。' : `识别 ${data.discovery.identified || 0} 部，未识别 ${data.discovery.unrecognized || 0} 个，新增队列 ${data.discovery.queued || 0} 项，已在队列 ${data.discovery.existing || 0} 项。`}</span>
        {data.discovery.error ? <span role="alert">{data.discovery.error}</span> : null}
        <span>此状态只表示扫描入队；整理结果和单项重试请到“处理队列”查看。</span>
      </section> : null}

      {activity.organizing ? <section className="javsp-progress-panel" aria-label="当前影片整理进度" aria-live="polite">
        <div className="javsp-progress-heading">
          <div><span>当前影片整理进度</span><strong>{progress.phaseLabel}</strong></div>
          <b>{progress.countLabel}</b>
        </div>
        <div className="javsp-progress-track" role="progressbar" aria-valuemin="0" aria-valuemax="100" aria-valuenow={progress.percentage}>
          <span style={{ width: `${progress.percentage}%` }} />
        </div>
        <p>{progress.detail || '引擎正在准备当前步骤…'}</p>
        <p>取消或重试请在处理队列中选择具体影片，不会影响下一部任务。</p>
      </section> : null}

      <div className="notice-box">
        <strong>目录授权与文件移动</strong>
        <span>使用“选择文件夹”可在当前存储空间内逐级返回上一级并选择可访问目录；其他存储空间需先在应用设置中授权。开启“移动影片文件”后，JAVSP 会按照输出模板重命名并移动文件。</span>
      </div>

      <div className="javsp-layout">
        <section className="settings-section">
          <div className="settings-title"><h2>扫描与输出</h2><p>目录、文件大小和整理方式等核心参数。</p></div>
          <div className="settings-grid">
            <div className="settings-field full-field">
              <span>扫描目录</span>
              <div className="folder-input-row">
                <Input aria-label="JAVSP 扫描目录" value={form.input_directory} onChange={(value) => set('input_directory', value)} />
                <Button htmlType="button" icon={<IconFolderOpen />} onClick={() => setFolderPickerTarget('input_directory')}>选择文件夹</Button>
              </div>
              <small>打开目录浏览器选择路径，也可以手动输入绝对路径</small>
            </div>
            <div className="settings-field full-field">
              <span>整理完成目录</span>
              <div className="folder-input-row">
                <Input aria-label="JAVSP 整理完成目录" value={form.output_directory || ''} onChange={(value) => set('output_directory', value)} placeholder="留空沿用扫描目录下的输出模板" />
                <Button htmlType="button" aria-label="选择完成目录" icon={<IconFolderOpen aria-hidden="true" />} onClick={() => setFolderPickerTarget('output_directory')}>选择完成目录</Button>
              </div>
              <small>可与扫描目录分开；目录必须已存在。跨卷搬运暂需人工处理，原地整理模式不使用此目录。</small>
            </div>
            <Field label="输出目录模板" hint={form.output_directory ? '相对于上面的整理完成目录，不要重复填写目录前缀' : '尚未指定独立目录：相对路径以扫描目录为基准'}><Input value={form.output_folder_pattern} onChange={(value) => set('output_folder_pattern', value)} /></Field>
            <Field label="最小文件大小"><Input value={form.minimum_size} onChange={(value) => set('minimum_size', value)} placeholder="1GiB" /></Field>
            <Field label="网络代理" hint="留空表示不使用代理"><Input value={form.proxy_server} onChange={(value) => set('proxy_server', value)} placeholder="http://127.0.0.1:7890" /></Field>
          </div>
          <div className="switch-stack">
            <Toggle label="移动影片文件" hint="按照输出目录和文件名模板整理影片；关闭时在原目录生成资料" checked={form.move_files} onChange={(value) => set('move_files', value)} />
          </div>
        </section>

        <JavspNamingPreview config={form} />

        <details className="javsp-advanced">
          <summary><span><strong>高级设置</strong><small>扫描规则、爬虫、命名、NFO 和图片</small></span><em aria-hidden="true">展开配置</em></summary>
          <div className="javsp-advanced-body">
        <section className="settings-section">
          <div className="settings-title"><h2>扫描规则</h2><p>控制哪些文件参与番号识别，以及哪些目录需要跳过。</p></div>
          <div className="settings-grid">
            <ListField label="影片扩展名" value={form.filename_extensions} onChange={(value) => set('filename_extensions', value)} />
            <ListField label="忽略的番号正则" value={form.ignored_id_patterns} onChange={(value) => set('ignored_id_patterns', value)} />
            <ListField label="忽略的目录名正则" value={form.ignored_folder_patterns} onChange={(value) => set('ignored_folder_patterns', value)} />
          </div>
          <div className="switch-stack">
            <Toggle label="跳过已有 NFO 的目录" hint="目录中已经存在 NFO 时不再整理" checked={form.skip_nfo_dir} onChange={(value) => set('skip_nfo_dir', value)} />
          </div>
        </section>

        <section className="settings-section">
          <div className="settings-title"><h2>网络与爬虫</h2><p>请求超时、重试、站点顺序和抓取并发。</p></div>
          <div className="settings-grid">
            <Field label="网络重试次数"><InputNumber min={0} max={20} value={form.network_retry} onChange={(value) => set('network_retry', value)} style={{ width: '100%' }} /></Field>
            <Field label="网络请求超时" hint="支持 10s 或 PT10S"><Input value={form.network_timeout} onChange={(value) => set('network_timeout', value)} /></Field>
            <Field label="影片并发数"><InputNumber min={1} max={64} value={form.movie_concurrency} onChange={(value) => set('movie_concurrency', value)} style={{ width: '100%' }} /></Field>
            <Field label="抓取启动间隔" hint="支持 1s 或 PT1S"><Input value={form.sleep_after_scraping} onChange={(value) => set('sleep_after_scraping', value)} /></Field>
            <Field label="javdb 封面策略"><Select value={form.use_javdb_cover} onChange={(value) => set('use_javdb_cover', value)} optionList={[{ value: 'fallback', label: '仅作后备' }, { value: 'yes', label: '优先使用' }, { value: 'no', label: '不使用' }]} /></Field>
            <Field label="fc2fan 本地镜像目录" hint="留空表示不用；填写时必须是已存在的绝对目录"><Input value={form.fc2fan_local_path} onChange={(value) => set('fc2fan_local_path', value)} /></Field>
            <ProxyFreeEditor value={form.proxy_free} onChange={(value) => set('proxy_free', value)} />
            <ListField label="普通影片爬虫顺序" value={form.normal_crawlers} onChange={(value) => set('normal_crawlers', value)} />
            <ListField label="FC2 爬虫顺序" value={form.fc2_crawlers} onChange={(value) => set('fc2_crawlers', value)} />
            <ListField label="CID 爬虫顺序" value={form.cid_crawlers} onChange={(value) => set('cid_crawlers', value)} />
            <ListField label="Getchu 爬虫顺序" value={form.getchu_crawlers} onChange={(value) => set('getchu_crawlers', value)} />
            <ListField label="Gyutto 爬虫顺序" value={form.gyutto_crawlers} onChange={(value) => set('gyutto_crawlers', value)} />
            <ListField label="抓取成功必需字段" value={form.required_keys} onChange={(value) => set('required_keys', value)} />
          </div>
          <div className="switch-stack">
            <Toggle label="努力抓取完整信息" hint="可能增加部分站点请求耗时" checked={form.hardworking} onChange={(value) => set('hardworking', value)} />
            <Toggle label="采用网站规范番号" hint="使用站点返回的番号规范化最终番号" checked={form.respect_site_avid} onChange={(value) => set('respect_site_avid', value)} />
            <Toggle label="规范女优别名" hint="使用内置别名表统一女优姓名" checked={form.normalize_actress_name} onChange={(value) => set('normalize_actress_name', value)} />
          </div>
        </section>

        <section className="settings-section">
          <div className="settings-title"><h2>文件命名与路径</h2><p>控制归档目录、媒体文件名和路径长度。</p></div>
          <div className="settings-grid">
            <Field label="媒体基础文件名模板"><Input value={form.path_basename_pattern} onChange={(value) => set('path_basename_pattern', value)} /></Field>
            <Field label="最大路径长度"><InputNumber min={1} max={4096} value={form.path_length_maximum} onChange={(value) => set('path_length_maximum', value)} style={{ width: '100%' }} /></Field>
            <Field label="女优人数上限"><InputNumber min={1} max={100} value={form.path_max_actress_count} onChange={(value) => set('path_max_actress_count', value)} style={{ width: '100%' }} /></Field>
          </div>
          <div className="switch-stack">
            <Toggle label="按字节计算路径长度" hint="关闭后按字符数计算" checked={form.path_length_by_byte} onChange={(value) => set('path_length_by_byte', value)} />
            <Toggle label="优先使用硬链接" hint="同一文件系统内尽量使用硬链接整理" checked={form.path_hard_link} onChange={(value) => set('path_hard_link', value)} />
            <Toggle label="移除标题末尾女优姓名" hint="清理标题末尾疑似重复的女优名称" checked={form.remove_trailing_actor_name} onChange={(value) => set('remove_trailing_actor_name', value)} />
          </div>
        </section>

        <section className="settings-section">
          <div className="settings-title"><h2>NFO 与缺省值</h2><p>设置 NFO 文件、标题格式和元数据缺失时的占位内容。</p></div>
          <div className="settings-grid">
            <Field label="NFO 基础文件名" hint="飞牛影视建议与媒体基础文件名保持一致；默认 {num}，视频入库前即可生成同名 NFO"><Input value={form.nfo_basename_pattern} onChange={(value) => set('nfo_basename_pattern', value)} /></Field>
            <Field label="NFO 标题模板"><Input value={form.nfo_title_pattern} onChange={(value) => set('nfo_title_pattern', value)} /></Field>
            <ListField label="NFO 类型字段" value={form.nfo_custom_genres_fields} onChange={(value) => set('nfo_custom_genres_fields', value)} />
            <ListField label="NFO 标签字段" value={form.nfo_custom_tags_fields} onChange={(value) => set('nfo_custom_tags_fields', value)} />
            <ListField label="打码状态文案" hint="依次为无码、有码、未知，必须正好 3 项" value={form.censor_options_representation} onChange={(value) => set('censor_options_representation', value)} />
            <Field label="未知标题"><Input value={form.default_title} onChange={(value) => set('default_title', value)} /></Field>
            <Field label="未知女优"><Input value={form.default_actress} onChange={(value) => set('default_actress', value)} /></Field>
            <Field label="未知系列"><Input value={form.default_series} onChange={(value) => set('default_series', value)} /></Field>
            <Field label="未知导演"><Input value={form.default_director} onChange={(value) => set('default_director', value)} /></Field>
            <Field label="未知制作商"><Input value={form.default_producer} onChange={(value) => set('default_producer', value)} /></Field>
            <Field label="未知发行商"><Input value={form.default_publisher} onChange={(value) => set('default_publisher', value)} /></Field>
          </div>
        </section>

        <section className="settings-section">
          <div className="settings-title"><h2>封面与剧照</h2><p>封面裁剪、标签、基础文件名和额外剧照下载。</p></div>
          <div className="settings-grid">
            <Field label="封面基础文件名"><Input value={form.cover_basename_pattern} onChange={(value) => set('cover_basename_pattern', value)} /></Field>
            <Field label="封面裁剪引擎"><Select value={form.cover_crop_engine} onChange={(value) => set('cover_crop_engine', value)} optionList={[{ value: 'pigo', label: 'Pigo（内置）' }, { value: 'slimeface', label: 'Slimeface' }, { value: 'none', label: '关闭人脸裁剪' }]} /></Field>
            <Field label="剧照基础文件名"><Input value={form.fanart_basename_pattern} onChange={(value) => set('fanart_basename_pattern', value)} /></Field>
            <Field label="额外剧照请求间隔" hint="支持 1.5s 或 PT1.5S"><Input value={form.extra_fanart_scrap_interval} onChange={(value) => set('extra_fanart_scrap_interval', value)} /></Field>
            <ListField label="启用裁剪的番号正则" value={form.cover_crop_on_id_patterns} onChange={(value) => set('cover_crop_on_id_patterns', value)} />
          </div>
          <div className="switch-stack">
            <Toggle label="下载高清封面" hint="优先保存站点提供的高分辨率封面" checked={form.cover_highres} onChange={(value) => set('cover_highres', value)} />
            <Toggle label="叠加字幕/无码标记" hint="在匹配影片的封面上叠加内置标签" checked={form.cover_add_label} onChange={(value) => set('cover_add_label', value)} />
            <Toggle label="下载额外剧照" hint="抓取站点提供的额外预览图片" checked={form.extra_fanarts} onChange={(value) => set('extra_fanarts', value)} />
          </div>
        </section>
          </div>
        </details>

        <section className="settings-section">
          <div className="settings-title"><h2>翻译</h2><p>密钥只保存在应用私有目录，读取页面时不会返回明文。</p></div>
          <div className="settings-grid">
            <Field label="翻译引擎"><Select value={form.translator_engine} onChange={(value) => set('translator_engine', value)} optionList={translatorOptions} /></Field>
            {form.translator_engine === 'openai' ? <Field label="兼容接口地址"><Input value={form.translator_url} onChange={(value) => set('translator_url', value)} placeholder="https://api.example.com/v1" /></Field> : null}
            {form.translator_engine === 'openai' ? <Field label="模型"><Input value={form.translator_model} onChange={(value) => set('translator_model', value)} /></Field> : null}
            {form.translator_engine === 'baidu' ? <Field label="App ID"><Input value={form.translator_app_id} onChange={(value) => set('translator_app_id', value)} /></Field> : null}
            {translatorNeedsAPIKey(form.translator_engine) ? <Field label="API Key" hint={form.translator_api_key_set && !form.translator_api_key ? '密钥已保存；留空表示保持不变' : '保存后不会再显示明文'}><Input mode="password" autoComplete="new-password" value={form.translator_api_key || ''} onChange={(value) => set('translator_api_key', value)} placeholder={form.translator_api_key_set ? '••••••••' : '输入 API Key'} /></Field> : null}
          </div>
          <div className="switch-stack">
            <Toggle label="翻译标题" hint="将影片标题交给选定翻译引擎" checked={form.translate_title} onChange={(value) => set('translate_title', value)} />
            <Toggle label="翻译剧情" hint="将剧情简介交给选定翻译引擎" checked={form.translate_plot} onChange={(value) => set('translate_plot', value)} />
          </div>
        </section>

        <section className="settings-section">
          <div className="settings-title"><h2>定时执行</h2><p>定时发现新的待整理文件并入队，独立于 RSS；已有队列不受此开关影响。</p></div>
          <div className="schedule-controls">
            <label className="switch-row setting-switch"><span><strong>启用定时扫描入队</strong><small>到达计划时间后发现文件；入队影片由队列依次整理</small></span><Switch checked={form.enabled} onChange={(value) => set('enabled', value)} /></label>
            <div className="settings-grid">
              <Field label="定时方式"><Select value={form.schedule_type} onChange={(value) => set('schedule_type', value)} optionList={[{ value: 'daily', label: '每天指定时间' }, { value: 'interval', label: '固定间隔' }]} /></Field>
              {form.schedule_type === 'daily' ? <Field label="每天执行时间" hint="NAS 系统时区"><Input type="time" value={form.schedule_time} onChange={(value) => set('schedule_time', value)} /></Field> : <Field label="执行间隔（分钟）"><InputNumber min={15} max={10080} value={form.schedule_minutes} onChange={(value) => set('schedule_minutes', value)} style={{ width: '100%' }} /></Field>}
              <Field label="单次整理最长运行时间（秒）" hint="不影响文件发现的独立超时限制"><InputNumber min={60} max={21600} value={form.timeout_seconds} onChange={(value) => set('timeout_seconds', value)} style={{ width: '100%' }} /></Field>
            </div>
          </div>
        </section>

        <div className="runtime-owned-note">
          <strong>由原生应用接管的运行参数</strong>
          <span>交互式确认、检查更新和自动更新固定关闭，避免后台任务等待输入或绕过飞牛应用升级流程。</span>
        </div>

        <div className="config-path"><span>配置文件</span><code>{data.config_path}</code><Button theme="solid" type="primary" aria-label="保存配置" icon={<IconSave aria-hidden="true" />} loading={saving} onClick={save}>保存配置</Button></div>
      </div>

      <div className="section-heading javsp-runs-heading"><h2>JAVSP 运行记录</h2></div>
      <Table rowKey="id" columns={columns} dataSource={data.runs || []} pagination={{ pageSize: 10 }} empty="暂无 JAVSP 运行记录" expandedRowRender={(item) => {
        return <pre className={`javsp-log ${item.error ? 'has-error' : ''}`}>{item.error ? `${item.error}\n` : ''}{javspRunOutput(item, data)}</pre>;
      }} />
      <FolderPicker
        key={folderPickerTarget || 'closed'}
        visible={Boolean(folderPickerTarget)}
        title={folderPickerTarget === 'output_directory' ? '选择 JAVSP 整理完成目录' : '选择 JAVSP 扫描目录'}
        loadDirectories={folderPickerTarget === 'output_directory' ? outputDirectories : api.javspDirectories}
        onCancel={() => setFolderPickerTarget(null)}
        onSelect={(path) => {
          if (folderPickerTarget) set(folderPickerTarget, path);
          setFolderPickerTarget(null);
        }}
      />
      <Modal
        visible={Boolean(confirmation)}
        title={confirmation?.title}
        okText={confirmation?.okText}
        okType={confirmation?.okType || 'primary'}
        okButtonProps={{ ...confirmation?.okButtonProps, disabled: confirming }}
        confirmLoading={confirming}
        cancelButtonProps={{ 'aria-label': '取消', disabled: confirming }}
        maskClosable={false}
        closable={!confirming}
        closeOnEsc={!confirming}
        onCancel={() => {
          if (!confirming && !configReload.current) setConfirmation(null);
        }}
        onOk={async () => {
          if (!confirmation) return;
          await confirmation.onOk();
          setConfirmation(null);
        }}
      >{confirmation?.content}</Modal>
    </section>
  );
}
