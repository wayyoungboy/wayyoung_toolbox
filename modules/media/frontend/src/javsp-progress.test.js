import { describe, expect, it } from 'vitest';
import { javspProgressModel } from './javsp-progress';

describe('javspProgressModel', () => {
  it('describes a running movie and clamps the percentage', () => {
    expect(javspProgressModel({
      phase: 'processing', movie: 'MIKR-069', site: 'javbus', message: '正在抓取',
      current: 2, total: 5, processed: 1,
    })).toEqual({
      phaseLabel: '整理影片', detail: 'MIKR-069 · javbus · 正在抓取',
      countLabel: '2 / 5（已完成 1）', percentage: 40, terminal: false,
    });
    expect(javspProgressModel({ phase: 'processing', current: 9, total: 5 }).percentage).toBe(100);
  });

  it('uses safe labels for canceled and unknown phases', () => {
    expect(javspProgressModel({ phase: 'canceled' }).phaseLabel).toBe('已取消');
    expect(javspProgressModel({ phase: '<script>' }).phaseLabel).toBe('处理中');
  });
});
