import { expect, it, vi } from 'vitest';
import { checkModule, overviewHTML } from '../../../../web/overview.js';

it.each(['backup', 'media', 'music', 'ha500', 'integrity'])('rejects incomplete %s status instead of reporting healthy', async id => {
  await expect(checkModule(id, async () => ({}))).rejects.toThrow('字段不完整');
});
it('reports unconfigured backup and music without leaking credentials or making writes', async () => {
  const request = vi.fn(async p => p.endsWith('state') ? { state: { tasks: [], runs: [] }, csrf: 'secret' } : { roots: [] });
  expect(await checkModule('backup', request)).toMatchObject({ level: 'warning', summary: '尚未创建备份任务' });
  expect(request.mock.calls.every(args => args.length === 1)).toBe(true);
  const result = await checkModule('music', async () => ({ source_dir: '', output_dir: '', password: 'secret' }));
  expect(result.level).toBe('warning');
  expect(JSON.stringify(result)).not.toContain('secret');
});
it('distinguishes configured, running, and failed states for each module', async () => {
  const backup = await checkModule('backup', async p => p.endsWith('state') ? { state: { tasks: [{}], runs: [{status: 'running'}] } } : {roots: [{}]});
  expect(backup.summary).toContain('1 个待执行/运行中');
  const media = await checkModule('media', async () => ({stages: [{label:'RSS', summary:'8 条待处理', status:'warning'}], paths:{consistent: true}}));
  expect(media.level).toBe('warning');
  const consistent = await checkModule('media', async () => ({stages: [{label:'qB', summary:'连接成功', status:'ok'}], paths:{consistent: true}}));
  expect(consistent.level).toBe('info');
  const inconsistent = await checkModule('media', async () => ({stages: [{status:'error', error:'private'}], paths:{consistent: false, issues:['目录不同']}}));
  expect(inconsistent.summary).toContain('不一致');
  expect(JSON.stringify(inconsistent)).not.toContain('private');
  expect((await checkModule('music', async () => ({source_dir:'/data', output_dir:'/out'}))).level).toBe('info');
  const hardware = { hardware:{ supported: true, fan_driver: true }, fans:[{ name:'fan', rpm:1000, verified:false, policy:{mode:'monitor'} }] };
  expect((await checkModule('ha500', async () => hardware)).level).toBe('info');
  expect((await checkModule('ha500', async () => ({...hardware,last_error:'failed'}))).level).toBe('warning');
  expect((await checkModule('integrity', async p => p.endsWith('config') ? { roots:['/data'] } : {running:false,damaged:1})).level).toBe('warning');
  expect((await checkModule('integrity', async p => p.endsWith('config') ? { roots:['/data'] } : {running:true,checked:5})).summary).toContain('正在');
});
it('escapes untrusted status text and timestamps inspection without making guarantees', () => {
  const html = overviewHTML([{id:'music',running:true}], new Map([['music',{summary:'<img onerror=x>',level:'error',details:['<script>'],checkedAt:new Date().toISOString()}]]), new Set());
  expect(html).not.toContain('<img'); expect(html).not.toContain('<script>');
  expect(html).toContain('仅代表当次结果');
});
