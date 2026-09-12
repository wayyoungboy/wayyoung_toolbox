package config

import "testing"

func TestFC2DBSelectionAccepted(t *testing.T) {
	if _, err := LoadWithOverrides(localConfigPath, nil, []string{"crawler.selection.fc2=[fc2,javmenu,fc2db]"}); err != nil {
		t.Fatal(err)
	}
}
