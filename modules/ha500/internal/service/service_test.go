package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/wayyoungboy/ha500-fnos-toolbox/internal/control"
	"github.com/wayyoungboy/ha500-fnos-toolbox/internal/hardware"
)

func writeServiceFixture(t *testing.T, root, relative, value string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func makeControllableHA500Fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, field := range []string{"sys_vendor", "product_name", "board_vendor", "board_name"} {
		writeServiceFixture(t, root, "class/dmi/id/"+field, "Default string\n")
	}
	writeServiceFixture(t, root, "class/dmi/id/chassis_type", "32\n")
	writeServiceFixture(t, root, "proc/cpuinfo", "model name: Intel N5105\n")
	controller := filepath.Join(root, "devices/pci0000:00/0000:02:00.0")
	writeServiceFixture(t, controller, "vendor", "0x1b21\n")
	writeServiceFixture(t, controller, "device", "0x1166\n")
	for port := 1; port <= 6; port++ {
		if err := os.MkdirAll(filepath.Join(controller, "ata"+strconv.Itoa(port)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "bus/pci/devices"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(controller, filepath.Join(root, "bus/pci/devices/0000:02:00.0")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "block"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeServiceFixture(t, root, "class/gpio/gpiochip512/label", "INT34C8:00\n")
	writeServiceFixture(t, root, "class/gpio/gpiochip512/base", "512\n")
	writeServiceFixture(t, root, "class/gpio/gpiochip512/ngpio", "300\n")
	for _, offset := range []int{75, 261, 100, 260, 256} {
		writeServiceFixture(t, root, "class/gpio/gpio"+strconv.Itoa(512+offset)+"/direction", "out\n")
		writeServiceFixture(t, root, "class/gpio/gpio"+strconv.Itoa(512+offset)+"/value", "1\n")
	}
	writeServiceFixture(t, root, "class/hwmon/hwmon0/name", "coretemp\n")
	writeServiceFixture(t, root, "class/hwmon/hwmon0/temp1_label", "Package id 0\n")
	writeServiceFixture(t, root, "class/hwmon/hwmon0/temp1_input", "45000\n")
	writeServiceFixture(t, root, "class/hwmon/hwmon1/name", "it8620\n")
	writeServiceFixture(t, root, "class/hwmon/hwmon1/fan3_input", "1800\n")
	writeServiceFixture(t, root, "class/hwmon/hwmon1/pwm3", "143\n")
	writeServiceFixture(t, root, "class/hwmon/hwmon1/pwm3_enable", "1\n")
	return root
}

type fakeDiskTemperatureProvider struct {
	items []hardware.DiskTemperature
}

func (provider fakeDiskTemperatureProvider) Snapshot(context.Context, map[int]hardware.Disk) ([]hardware.DiskTemperature, error) {
	return provider.items, nil
}

func TestServiceStartsFailClosedWithSafeDefaults(t *testing.T) {
	root := t.TempDir()
	service, err := New(Options{
		SysRoot:       root,
		CPUInfoPath:   filepath.Join(root, "proc/cpuinfo"),
		SettingsPath:  filepath.Join(root, "var/settings.json"),
		LogPath:       filepath.Join(root, "var/toolbox.log"),
		Version:       "0.1.0",
		ConflictCheck: func() string { return "" },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	settings := service.Settings()
	if settings.BayPortMap != [5]int{1, 2, 3, 4, 5} || len(settings.FanPolicies) != 0 {
		t.Fatalf("unsafe defaults: %+v", settings)
	}
	status, err := service.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Hardware.Supported || !status.MonitorOnly || status.Version != "0.1.0" {
		t.Fatalf("status = %+v; want unsupported monitor-only service", status)
	}
}

func TestStatusPublishesDiskTemperatureSourcesWithoutPlainSerial(t *testing.T) {
	root := t.TempDir()
	source := hardware.DiskTemperatureSourceID("PRIVATE-SERIAL-1234")
	service, err := New(Options{
		SysRoot:      root,
		CPUInfoPath:  filepath.Join(root, "proc/cpuinfo"),
		SettingsPath: filepath.Join(root, "var/settings.json"),
		LogPath:      filepath.Join(root, "var/toolbox.log"),
		Version:      "0.2.0",
		DiskTemperatures: fakeDiskTemperatureProvider{items: []hardware.DiskTemperature{
			{SourceID: source, DisplayID: "A1B2C3", Device: "sda", Model: "Example Disk", ControllerPort: 1, TemperatureC: 40, Available: true},
		}},
		ConflictCheck: func() string { return "" },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	status, err := service.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(status.TemperatureSources) != 2 || status.TemperatureSources[1].ID != source || status.TemperatureSources[1].TemperatureC == nil || *status.TemperatureSources[1].TemperatureC != 40 {
		t.Fatalf("temperature sources = %+v", status.TemperatureSources)
	}
	if status.TemperatureSources[1].Label == "" {
		t.Fatal("disk temperature source has no human-readable label")
	}
}

func TestSetFanPolicyRequiresAnAvailableDiskTemperatureSource(t *testing.T) {
	source := hardware.DiskTemperatureSourceID("PRIVATE-SERIAL-1234")
	for _, test := range []struct {
		name      string
		available bool
		wantErr   bool
	}{
		{name: "available", available: true},
		{name: "unavailable", available: false, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := makeControllableHA500Fixture(t)
			settingsPath := filepath.Join(root, "var/settings.json")
			writeServiceFixture(t, root, "var/settings.json", `{"bay_port_map":[1,2,3,4,5],"fan_policies":{},"verified_fans":{"3":true}}`)
			service, err := New(Options{
				SysRoot: root, CPUInfoPath: filepath.Join(root, "proc/cpuinfo"), SettingsPath: settingsPath,
				LogPath: filepath.Join(root, "var/toolbox.log"), Version: "0.2.0", ConflictCheck: func() string { return "" },
				DiskTemperatures: fakeDiskTemperatureProvider{items: []hardware.DiskTemperature{{
					SourceID: source, DisplayID: "A1B2C3", Model: "Disk", ControllerPort: 1, TemperatureC: 40, Available: test.available,
				}}},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer service.Close()
			policy := control.FanPolicy{Mode: control.ModeFixed, TemperatureSource: source, MinDutyPercent: 35, FixedDutyPercent: 56, CriticalTemperatureC: 80}
			err = service.SetFanPolicy(context.Background(), 3, policy)
			if test.wantErr {
				if !errors.Is(err, control.ErrTemperatureUnavailable) {
					t.Fatalf("SetFanPolicy error = %v; want ErrTemperatureUnavailable", err)
				}
				return
			}
			if err != nil || service.Settings().FanPolicies[3].TemperatureSource != source {
				t.Fatalf("SetFanPolicy error = %v, settings = %+v", err, service.Settings())
			}
		})
	}
}

func TestConflictPreventsVerificationAndControlledPolicy(t *testing.T) {
	root := t.TempDir()
	service, err := New(Options{
		SysRoot:       root,
		CPUInfoPath:   filepath.Join(root, "proc/cpuinfo"),
		SettingsPath:  filepath.Join(root, "var/settings.json"),
		LogPath:       filepath.Join(root, "var/toolbox.log"),
		Version:       "0.1.0",
		ConflictCheck: func() string { return "coolercontrold" },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if _, err := service.VerifyFan(context.Background(), 3, false); !errors.Is(err, ErrControlConflict) {
		t.Fatalf("VerifyFan error = %v; want ErrControlConflict", err)
	}
	policy := control.FanPolicy{Mode: control.ModeFixed, TemperatureSource: "cpu", MinDutyPercent: 35, FixedDutyPercent: 56, CriticalTemperatureC: 80}
	if err := service.SetFanPolicy(context.Background(), 3, policy); !errors.Is(err, ErrControlConflict) {
		t.Fatalf("SetFanPolicy error = %v; want ErrControlConflict", err)
	}
	if err := service.SetFanPolicy(context.Background(), 3, control.FanPolicy{Mode: control.ModeMonitor}); err != nil {
		t.Fatalf("monitor policy should remain available during a conflict: %v", err)
	}
}

func TestUnsupportedHardwareCannotVerifyOrTakeFanControl(t *testing.T) {
	root := t.TempDir()
	service, err := New(Options{
		SysRoot:       root,
		CPUInfoPath:   filepath.Join(root, "proc/cpuinfo"),
		SettingsPath:  filepath.Join(root, "var/settings.json"),
		LogPath:       filepath.Join(root, "var/toolbox.log"),
		Version:       "0.1.0",
		ConflictCheck: func() string { return "" },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if _, err := service.VerifyFan(context.Background(), 3, false); !errors.Is(err, ErrUnsupportedHardware) {
		t.Fatalf("VerifyFan error = %v; want ErrUnsupportedHardware", err)
	}
	policy := control.FanPolicy{Mode: control.ModeFixed, TemperatureSource: "cpu", MinDutyPercent: 35, FixedDutyPercent: 56, CriticalTemperatureC: 80}
	if err := service.SetFanPolicy(context.Background(), 3, policy); !errors.Is(err, ErrUnsupportedHardware) {
		t.Fatalf("SetFanPolicy error = %v; want ErrUnsupportedHardware", err)
	}
}
