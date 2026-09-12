package config

import "testing"

func TestValidateDeepLAcceptsAPIKey(t *testing.T) {
	if _, err := LoadWithOverrides(localConfigPath, nil, []string{"translator.engine={name: deepl, api_key: 'test-key:fx'}"}); err != nil {
		t.Fatalf("DeepL engine rejected: %v", err)
	}
}
