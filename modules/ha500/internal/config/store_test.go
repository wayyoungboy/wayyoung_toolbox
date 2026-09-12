package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/wayyoungboy/ha500-fnos-toolbox/internal/control"
)

func TestStoreRoundTripsPrivateSettingsAtomically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	store := NewStore(path)
	want := Settings{
		BayPortMap: [5]int{2, 1, 3, 5, 4},
		FanPolicies: control.PolicySet{
			2: {Mode: control.ModeFixed, FixedDutyPercent: 100, MinDutyPercent: 35, CriticalTemperatureC: 80, TemperatureSource: "cpu"},
			3: {Mode: control.ModeCurve, MinDutyPercent: 35, CriticalTemperatureC: 80, TemperatureSource: "cpu", Curve: control.Curve{{TemperatureC: 40, DutyPercent: 35}, {TemperatureC: 80, DutyPercent: 100}}},
		},
	}
	if err := store.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Load = %+v; want %+v", got, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("settings mode = %o; want 600", info.Mode().Perm())
	}
}

func TestStoreReturnsSafeDefaultsWhenSettingsDoNotExist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	got, err := NewStore(path).LoadOrDefault()
	if err != nil {
		t.Fatal(err)
	}
	if got.BayPortMap != [5]int{1, 2, 3, 4, 5} {
		t.Fatalf("default bay map = %v", got.BayPortMap)
	}
	if got.FanPolicies == nil || len(got.FanPolicies) != 0 {
		t.Fatalf("default fan policies = %#v; want an empty monitor-only set", got.FanPolicies)
	}
}
