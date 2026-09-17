import Table from '@douyinfe/semi-ui/lib/es/table';

export default function OrphanFoldersResults({ result }) {
  const columns = [
    { title: '影片目录', dataIndex: 'path', render: (path) => <span className="maintenance-path">{path}</span> },
    { title: '附件', render: (_, folder) => <details><summary>{folder.files.length} 个 · {(folder.bytes / 1024 / 1024).toFixed(2)} MB</summary><ul>{folder.files.map((path) => <li className="maintenance-path" key={path}>{path}</li>)}</ul></details> },
    { title: '处理状态', render: (_, folder) => folder.skip_reason || (result.directories?.includes(folder.path) ? '已清理' : '可清理：无视频，仅有影片附件') },
  ];
  return <div className="maintenance-results">
    <p>符合条件 {result.found} 个目录，已清理 {result.removed || 0} 个目录 / {result.deleted_files || 0} 个附件</p>
    <Table rowKey="path" columns={columns} dataSource={result.candidates || []} pagination={{ pageSize: 10 }} empty="未发现无视频残留目录" />
  </div>;
}
