export const translatorOptions = [
  { value: 'none', label: '不翻译' },
  { value: 'google', label: 'Google 翻译' },
  { value: 'deepl', label: 'DeepL' },
  { value: 'bing', label: 'Bing' },
  { value: 'baidu', label: '百度翻译' },
  { value: 'claude', label: 'Claude' },
  { value: 'openai', label: 'OpenAI 兼容接口' },
];

const apiKeyTranslators = new Set(['deepl', 'bing', 'baidu', 'claude', 'openai']);

export function translatorNeedsAPIKey(engine) {
  return apiKeyTranslators.has(engine);
}

export function javspPayload(config) {
  return {
    ...config,
    translator_api_key: config.translator_api_key || (config.translator_api_key_set ? '__UNCHANGED__' : ''),
  };
}
