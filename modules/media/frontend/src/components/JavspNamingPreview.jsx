import { useEffect, useRef, useState } from 'react';
import Button from '@douyinfe/semi-ui/lib/es/button';
import { api } from '../api';
import NamingSampleFields from './NamingSampleFields';
import NamingPreviewResult from './NamingPreviewResult';

const initialSample = { media_id: 'TEST-001', title: '示例影片标题', actress: ['示例演员'], publish_date: '2026-01-02', filenames: ['TEST-001-C.mp4'] };
const splitList = value => value.split(',').map(item => item.trim()).filter(Boolean);

export default function JavspNamingPreview({ config }) {
 const [sample, setSample] = useState(initialSample);
 const [actressText, setActressText] = useState(initialSample.actress.join(','));
 const [filenamesText, setFilenamesText] = useState(initialSample.filenames.join(','));
 const [result, setResult] = useState(null);
 const [loading, setLoading] = useState(false);
 const request = useRef(null);
 // A preview never needs a translator credential. Keep it out of both the
 // request and the result identity, without changing the editable form.
 const draft = { config: { ...config, translator_api_key: '' }, sample: { ...sample, actress: splitList(actressText), filenames: splitList(filenamesText) } };
 const key = JSON.stringify({ ...draft, actressText, filenamesText });
 const current = result?.key === key ? result : null;
 useEffect(() => () => { request.current?.abort(); request.current = null; }, []);
 const set = (field, value) => setSample(previous => ({ ...previous, [field]: value }));
 const calculate = async () => {
  if (request.current) return;
  const controller = new AbortController();
  request.current = controller;
  setLoading(true);
  try {
   const data = await api.previewJavspNames(draft, { signal: controller.signal });
   if (!controller.signal.aborted && request.current === controller) setResult({ key, data });
  } catch (error) {
   if (!controller.signal.aborted && request.current === controller) setResult({ key, error: error.message });
  } finally {
   if (request.current === controller) { request.current = null; setLoading(false); }
  }
 };
 return <section className="settings-section naming-preview">
  <div className="settings-title"><h2>命名预览</h2><p>使用当前未保存的规则与示例元数据计算。不会访问站点、扫描目录、保存配置或移动文件。真实结果取决于刮削取得的元数据。</p></div>
  <div className="settings-grid">
   <NamingSampleFields sample={sample} actressText={actressText} filenamesText={filenamesText} onChange={set} onActressChange={setActressText} onFilenamesChange={setFilenamesText} />
   <div className="full-field"><Button htmlType="button" aria-label="预览最终路径" disabled={loading} loading={loading} onClick={calculate}>预览最终路径</Button></div>
   {result && !current ? <p className="full-field muted-text">配置或示例已变更，请重新预览。</p> : null}
   {current?.error ? <p className="full-field danger-text" role="alert">{current.error}</p> : null}
   {current?.data ? <NamingPreviewResult data={current.data} /> : null}
  </div>
 </section>;
}
