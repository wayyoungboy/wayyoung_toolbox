import { useEffect, useMemo, useState } from 'react';
import Button from '@douyinfe/semi-ui/lib/es/button';
import Input from '@douyinfe/semi-ui/lib/es/input';
import InputNumber from '@douyinfe/semi-ui/lib/es/inputNumber';
import Modal from '@douyinfe/semi-ui/lib/es/modal';
import Select from '@douyinfe/semi-ui/lib/es/select';
import Table from '@douyinfe/semi-ui/lib/es/table';
import Toast from '@douyinfe/semi-ui/lib/es/toast';
import { IconFolderOpen, IconRefresh, IconSave, IconScan } from '@douyinfe/semi-icons';
import { api, formatDate } from '../api';
import FolderPicker from '../components/FolderPicker';

function Summary({ label, value, hint, tone }) {
  return <div className="stat-block"><div className={`stat-value ${tone || ''}`}>{value}</div><div className="stat-label">{label}</div>{hint ? <div className="stat-hint">{hint}</div> : null}</div>;
}

export default function Anime() {
  const [data, setData] = useState(null);
  const [directory, setDirectory] = useState('');
  const [result, setResult] = useState(null);
  const [filter, setFilter] = useState('all');
  const [pickerVisible, setPickerVisible] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [scanning, setScanning] = useState(false);
  const [writing, setWriting] = useState(false);

  const load = async () => {
    try {
      const next = await api.anime();
      setData(next);
      setDirectory(next.config.directory);
    } catch (error) { Toast.error(error.message); }
  };
  useEffect(() => { load(); }, []);

  const refreshTitles = async () => {
    setRefreshing(true);
    try {
      const cache = await api.refreshAnimeTitles();
      setData((current) => ({ ...current, cache }));
      Toast.success(`AniDB 标题库已更新，共 ${cache.title_count} 条标题`);
    } catch (error) { Toast.error(error.message); }
    finally { setRefreshing(false); }
  };

  const scan = async () => {
    setScanning(true);
    try {
      await api.updateAnime({ directory });
      const next = await api.scanAnime(directory);
      setResult(next);
      setData((current) => ({ ...current, config: { directory }, cache: next.cache }));
      Toast.success(`扫描完成：${next.matched}/${next.total} 个文件已匹配`);
    } catch (error) { Toast.error(error.message); }
    finally { setScanning(false); }
  };

  const updateItem = (filePath, changes) => setResult((current) => ({
    ...current,
    items: current.items.map((item) => item.file_path === filePath ? { ...item, ...changes } : item),
  }));

  const writableItems = useMemo(() => (result?.items || []).filter((item) => (
    !item.existing_nfo && item.aid > 0 && item.confidence >= 90 && item.matched_title
  )), [result]);

  const writeNFO = () => Modal.confirm({
    title: `生成 ${writableItems.length} 个同名 NFO？`,
    content: '只为高置信度或人工确认的匹配生成同名 NFO；已有 NFO 不会被覆盖，原视频不会移动或重命名。',
    okText: '生成 NFO',
    onOk: async () => {
      setWriting(true);
      try {
        const outcome = await api.writeAnimeNFO({ directory, overwrite: false, items: writableItems });
        Toast.success(`已生成 ${outcome.created} 个，跳过 ${outcome.skipped} 个，失败 ${outcome.failed} 个`);
        await scan();
      } catch (error) { Toast.error(error.message); }
      finally { setWriting(false); }
    },
  });

  const shownItems = useMemo(() => (result?.items || []).filter((item) => {
    if (filter === 'matched') return item.aid > 0 && item.confidence >= 90;
    if (filter === 'unmatched') return !item.aid;
    if (filter === 'missing_nfo') return !item.existing_nfo;
    return true;
  }), [filter, result]);

  const columns = [
    {
      title: '文件', dataIndex: 'file_name', width: 310,
      render: (value, item) => <div><strong>{value}</strong><div className="cell-secondary" title={item.file_path}>{item.file_path}</div></div>,
    },
    {
      title: '解析结果', width: 220,
      render: (_, item) => <div>{item.parsed_title || '--'}<div className="cell-secondary">{[item.studio, item.release_date, item.episode ? `第 ${item.episode} 集` : ''].filter(Boolean).join(' · ') || '--'}</div></div>,
    },
    {
      title: 'AniDB AID', width: 120,
      render: (_, item) => <InputNumber min={1} value={item.aid || undefined} placeholder="未匹配" onChange={(aid) => updateItem(item.file_path, { aid: aid || 0, confidence: aid && item.matched_title ? 100 : 0, match_method: aid ? 'manual' : '' })} style={{ width: '100%' }} />,
    },
    {
      title: '确认标题', width: 260,
      render: (_, item) => <Input value={item.matched_title || ''} placeholder="输入 AniDB 标题后人工确认" onChange={(matchedTitle) => updateItem(item.file_path, { matched_title: matchedTitle, confidence: matchedTitle && item.aid ? 100 : 0, match_method: matchedTitle && item.aid ? 'manual' : '' })} />,
    },
    {
      title: '匹配', width: 110,
      render: (_, item) => item.aid ? <span className={item.confidence >= 90 ? 'success-text' : ''}>{item.match_method === 'manual' ? '人工确认' : `${item.confidence}%`}</span> : <span className="danger-text">待处理</span>,
    },
    { title: 'NFO', width: 90, render: (_, item) => item.existing_nfo ? <span className="success-text">已有</span> : '缺少' },
  ];

  if (!data) return <div className="center-state">正在读取番剧刮削配置…</div>;
  return (
    <section className="page anime-page">
      <header className="page-header">
        <div><h1>番剧助手</h1><p>使用 AniDB 官方标题库识别本地番剧，并生成飞牛影视可读取的同名 NFO</p></div>
        <div className="header-actions">
          <Button icon={<IconRefresh />} loading={refreshing} onClick={refreshTitles}>更新标题库</Button>
          <Button theme="solid" type="primary" icon={<IconScan />} loading={scanning} onClick={scan}>扫描匹配</Button>
        </div>
      </header>

      <div className="notice-box anime-notice">
        <strong>当前使用官方每日标题库</strong>
        <span>可完成标题与 AniDB AID 匹配；简介、演员和海报需要注册 AniDB HTTP API client，当前版本不会进行网页抓取。扫描本身只读，生成 NFO 前会再次确认。</span>
      </div>

      <div className="anime-directory-bar">
        <div><strong>扫描目录</strong><span>默认读取现有番剧目录，不移动或重命名视频</span></div>
        <Input aria-label="番剧扫描目录" value={directory} onChange={setDirectory} />
        <Button icon={<IconFolderOpen />} onClick={() => setPickerVisible(true)}>选择文件夹</Button>
      </div>

      <div className="overview-strip anime-overview">
        <Summary label="视频总数" value={result?.total ?? '--'} hint={result ? `目录：${result.directory}` : '尚未扫描'} />
        <Summary label="高置信度匹配" value={result?.matched ?? '--'} hint="精确或最长标题前缀" tone="success-text" />
        <Summary label="待人工处理" value={result ? result.review + result.unmatched : '--'} hint="可在结果表中填写 AID 与标题" tone={result?.unmatched ? 'danger-text' : ''} />
        <Summary label="标题库更新时间" value={formatDate(result?.cache?.updated_at || data.cache?.updated_at)} hint={result?.cache?.title_count ? `${result.cache.title_count} 条标题` : '首次扫描时自动下载'} />
      </div>

      <div className="section-heading anime-result-heading">
        <h2>匹配结果</h2>
        <div className="header-actions">
          <Select value={filter} onChange={setFilter} style={{ width: 150 }} optionList={[
            { value: 'all', label: '全部文件' }, { value: 'matched', label: '已匹配' },
            { value: 'unmatched', label: '未匹配' }, { value: 'missing_nfo', label: '缺少 NFO' },
          ]} />
          <Button icon={<IconSave />} loading={writing} disabled={!writableItems.length} onClick={writeNFO}>生成同名 NFO（{writableItems.length}）</Button>
        </div>
      </div>
      <Table rowKey="file_path" columns={columns} dataSource={shownItems} pagination={{ pageSize: 30 }} scroll={{ x: 1110 }} empty="请先扫描番剧目录" />

      <FolderPicker
        visible={pickerVisible}
        title="选择番剧扫描目录"
        loadDirectories={api.animeDirectories}
        onCancel={() => setPickerVisible(false)}
        onSelect={(path) => { setDirectory(path); setPickerVisible(false); }}
      />
    </section>
  );
}
