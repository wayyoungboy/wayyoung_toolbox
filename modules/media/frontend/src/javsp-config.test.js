import { describe, expect, it } from 'vitest';
import { javspPayload, translatorNeedsAPIKey, translatorOptions } from './javsp-config';

describe('javspPayload', () => {
  it('preserves a saved translator secret when the password field is untouched', () => {
    expect(javspPayload({ translator_api_key: '', translator_api_key_set: true })).toEqual({
      translator_api_key: '__UNCHANGED__',
      translator_api_key_set: true,
    });
  });

  it('sends a newly entered translator secret', () => {
    expect(javspPayload({ translator_api_key: 'new-secret', translator_api_key_set: true })).toEqual({
      translator_api_key: 'new-secret',
      translator_api_key_set: true,
    });
  });

	 it('exposes DeepL as an API-key translation engine', () => {
	   expect(translatorOptions).toContainEqual({ value: 'deepl', label: 'DeepL' });
	   expect(translatorNeedsAPIKey('deepl')).toBe(true);
	 });
});
