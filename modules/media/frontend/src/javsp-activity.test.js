import { describe, expect, it } from 'vitest';
import { javspActivity, javspRunOutput, javspTriggerLabel } from './javsp-activity';

describe('independent JAVSP discovery and organization', () => {
  it('allows discovery while organization is busy', () => {
    expect(javspActivity({ running: true, organizing: true, component: { available: true } })).toMatchObject({
      organizing: true, scanning: false, canScan: true, cancelScanRunID: null,
    });
  });
  it('only cancels the observed scan, not the independent organizer', () => {
    expect(javspActivity({ running: true, organizing: false, component: { available: true }, discovery: { status: 'running', run_id: 42 } })).toMatchObject({
      organizing: false, scanning: true, canScan: false, cancelScanRunID: 42,
    });
    expect(javspActivity({ organizing: true, component: { available: true }, discovery: { status: 'running', run_id: 43 } }).organizing).toBe(true);
  });
  it('does not offer a duplicate scan or a cancellation with an invalid run ID', () => {
    for (const run_id of [0, -1, undefined, '42', 1.5, Infinity]) {
      expect(javspActivity({ component: { available: true }, discovery: { status: 'running', run_id } })).toMatchObject({ canScan: false, cancelScanRunID: null });
    }
    expect(javspActivity(null).canScan).toBe(false);
    expect(javspActivity({ component: { available: false } }).canScan).toBe(false);
    expect(javspActivity({ component: { available: true }, discovery: { status: 'canceled', run_id: 42 } }).canScan).toBe(true);
  });
});

describe('JAVSP run lineage and output', () => {
  it('labels file discovery separately from queue execution', () => {
    expect(['manual_scan', 'schedule_scan', 'queue', 'manual', 'schedule'].map(javspTriggerLabel)).toEqual(['手动扫描', '定时扫描', '队列整理', '手动整理', '定时整理']);
    expect(javspTriggerLabel('future')).toBe('future');
  });
  it('never shows another film’s live organizer output in a discovery record', () => {
    const data = { organizing: true, live_output: 'TEST-OTHER 正在翻译' };
    for (const trigger_type of ['manual_scan', 'schedule_scan']) {
      const output = javspRunOutput({ trigger_type, status: 'running' }, data);
      expect(output).toContain('正在扫描');
      expect(output).not.toContain('TEST-OTHER');
    }
    expect(javspRunOutput({ trigger_type: 'queue', status: 'running' }, data)).toBe(data.live_output);
    expect(javspRunOutput({ trigger_type: 'queue', status: 'running', output: '已记录的输出' }, { ...data, organizing: false })).toBe('已记录的输出');
    expect(javspRunOutput({ trigger_type: 'manual_scan', status: 'success', output: '新增队列 2 项' }, data)).toBe('新增队列 2 项');
  });
});
