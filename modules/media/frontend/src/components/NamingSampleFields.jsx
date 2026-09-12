import Input from '@douyinfe/semi-ui/lib/es/input';

export default function NamingSampleFields({ sample, actressText, filenamesText, onChange, onActressChange, onFilenamesChange, disabled = false }) {
  return <>
    <label className="settings-field"><span>示例番号</span><Input aria-label="示例番号" value={sample.media_id} disabled={disabled} onChange={value => onChange('media_id', value)} /></label>
    <label className="settings-field"><span>示例标题</span><Input aria-label="示例标题" value={sample.title} disabled={disabled} onChange={value => onChange('title', value)} /></label>
    <label className="settings-field"><span>示例演员</span><Input aria-label="示例演员" value={actressText} disabled={disabled} onChange={onActressChange} /></label>
    <label className="settings-field"><span>示例发行日期</span><Input aria-label="示例发行日期" type="date" value={sample.publish_date} disabled={disabled} onChange={value => onChange('publish_date', value)} /></label>
    <label className="settings-field full-field"><span>示例文件名</span><Input aria-label="示例文件名" value={filenamesText} disabled={disabled} onChange={onFilenamesChange} /><small>多个分段用英文逗号分隔；只填文件名，不填目录。文件不需要真实存在。</small></label>
  </>;
}
