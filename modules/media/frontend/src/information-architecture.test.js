import { describe, expect, it } from 'vitest';
import {
  pipelineStageTab,
  pipelineFailureStages,
  pipelineTabs,
  primaryNavigation,
  runModuleOptions,
  unifiedRunModeLabel,
  resolveDestination,
} from './information-architecture';

describe('native information architecture', () => {
  it('makes the personal catalog primary without removing acquisition workspaces', () => {
    expect(primaryNavigation.map((item) => item.key)).toEqual([
      'catalog',
      'people',
      'personal',
      'dashboard',
      'subscriptions',
      'resources',
      'queue',
      'maintenance',
      'settings',
    ]);
    expect(primaryNavigation.map((item) => item.label)).toEqual([
      '馆藏',
      '人物',
      '个人记录',
      '工作台',
      '订阅来源',
      '资源搜索',
      '处理队列',
      '媒体维护',
      '设置',
    ]);
  });

  it('keeps old bookmarks and secondary tools reachable without extra primary destinations', () => {
    expect(resolveDestination('tasks')).toEqual({ page: 'subscriptions', tab: '' });
    expect(resolveDestination('runs')).toEqual({ page: 'queue', tab: 'history' });
    expect(resolveDestination('pipeline', 'intake')).toEqual({ page: 'settings', tab: 'intake' });
    expect(resolveDestination('javsp')).toEqual({ page: 'settings', tab: 'javsp' });
    expect(resolveDestination('anime')).toEqual({ page: 'settings', tab: 'anime' });
    expect(resolveDestination('queue', 'bad')).toEqual({ page: 'queue', tab: 'jobs' });
    expect(resolveDestination('missing')).toEqual({ page: 'catalog', tab: '' });
    expect(resolveDestination(null)).toEqual({ page: 'catalog', tab: '' });
    expect(resolveDestination('dashboard')).toEqual({ page: 'dashboard', tab: '' });
    expect(resolveDestination('people')).toEqual({page:'people',tab:''});
    expect(resolveDestination('resources')).toEqual({page:'resources',tab:''});
    expect(resolveDestination('personal','lists')).toEqual({page:'personal',tab:'lists'});
    expect(resolveDestination('personal','taste')).toEqual({page:'personal',tab:'taste'});
  });

  it('keeps the four acquisition stages inside one pipeline workspace', () => {
    expect(pipelineTabs.map((item) => item.key)).toEqual(['overview', 'rss', 'intake', 'javsp']);
    expect(pipelineTabs.map((item) => item.label)).toEqual(['流程概览', 'RSS 来源', '下载与入库', 'JAVSP 整理']);
  });

  it('routes every live pipeline stage to an existing workspace tab', () => {
    expect(['rss', 'qb', 'intake', 'javsp', 'catalog'].map(pipelineStageTab)).toEqual([
      'rss',
      'overview',
      'intake',
      'javsp',
      'catalog',
    ]);
    expect(resolveDestination('pipeline', 'catalog')).toEqual({ page: 'queue', tab: 'jobs' });
  });

  it('offers every workspace module in the unified run filter', () => {
    expect(runModuleOptions.map((item) => item.value)).toEqual([
      '',
      'rss',
      'intake',
      'javsp',
      'maintenance',
    ]);
  });

  it('keeps RSS preview and real submission distinguishable in unified history', () => {
    expect(unifiedRunModeLabel({ module: 'rss', dry_run: true })).toBe('预览');
    expect(unifiedRunModeLabel({ module: 'rss', dry_run: false })).toBe('正式');
    expect(unifiedRunModeLabel({ module: 'javsp' })).toBe('--');
  });

  it('turns a pipeline load failure into persistent stage errors', () => {
    const stages = pipelineFailureStages('服务暂时不可用');
    expect(stages).toHaveLength(5);
    expect(stages.every((stage) => stage.status === 'error')).toBe(true);
    expect(stages.every((stage) => stage.error === '服务暂时不可用')).toBe(true);
  });
});
