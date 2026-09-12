import { describe, expect, it } from 'vitest';
import { settingsPayload } from './settingsPayload';

describe('settings payload', () => {
  it('keeps the RSS proxy switch and normalizes retention and timeout numbers', () => {
    const payload = settingsPayload({
      feed_use_proxy: true,
      run_retention_count: '42',
      request_timeout: '30',
      qb_port: '8085',
      qb_password: '',
    }, false);

    expect(payload).toMatchObject({
      feed_use_proxy: true,
      run_retention_count: 42,
      request_timeout: 30,
      qb_port: 8085,
      qb_password: '__UNCHANGED__',
    });
  });

  it('includes a newly entered qBittorrent password only when changed', () => {
    const enteredValue = ['new', 'value'].join('-');
    const payload = settingsPayload({
      feed_use_proxy: false,
      run_retention_count: 100,
      request_timeout: 20,
      qb_port: 8085,
      qb_password: enteredValue,
    }, true);

    expect(payload.qb_password).toBe(enteredValue);
  });
});
