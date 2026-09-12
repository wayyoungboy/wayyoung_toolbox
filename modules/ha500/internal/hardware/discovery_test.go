package hardware

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeFixture(t *testing.T, root, relative, value string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create fixture directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(value), mode); err != nil {
		t.Fatalf("write fixture %s: %v", relative, err)
	}
	return path
}

func TestDiscoverFansFindsOnlyConnectedControllableChannels(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "class/hwmon/hwmon0/name", "coretemp\n", 0o444)
	writeFixture(t, root, "class/hwmon/hwmon0/temp1_label", "Package id 0\n", 0o444)
	writeFixture(t, root, "class/hwmon/hwmon0/temp1_input", "42000\n", 0o444)
	writeFixture(t, root, "class/hwmon/hwmon1/name", "it8620\n", 0o444)
	writeFixture(t, root, "class/hwmon/hwmon1/pwm1", "128\n", 0o644)
	writeFixture(t, root, "class/hwmon/hwmon1/pwm1_enable", "1\n", 0o644)
	writeFixture(t, root, "class/hwmon/hwmon1/fan2_input", "3550\n", 0o444)
	writeFixture(t, root, "class/hwmon/hwmon1/pwm2", "255\n", 0o644)
	writeFixture(t, root, "class/hwmon/hwmon1/pwm2_enable", "1\n", 0o644)
	writeFixture(t, root, "class/hwmon/hwmon1/fan3_input", "1790\n", 0o444)
	writeFixture(t, root, "class/hwmon/hwmon1/pwm3", "143\n", 0o644)
	writeFixture(t, root, "class/hwmon/hwmon1/pwm3_enable", "1\n", 0o644)
	writeFixture(t, root, "class/hwmon/hwmon1/fan4_input", "0\n", 0o444)
	writeFixture(t, root, "class/hwmon/hwmon1/pwm4", "127\n", 0o644)
	writeFixture(t, root, "class/hwmon/hwmon1/pwm4_enable", "2\n", 0o644)

	got, err := DiscoverFans(root)
	if err != nil {
		t.Fatalf("DiscoverFans returned error: %v", err)
	}
	if got.ChipName != "it8620" {
		t.Fatalf("chip name = %q; want it8620", got.ChipName)
	}
	if got.Temperature.Source != "coretemp" || got.Temperature.Label != "Package id 0" || got.Temperature.MilliCelsius != 42000 {
		t.Fatalf("temperature = %+v; want coretemp Package id 0 at 42000 mC", got.Temperature)
	}
	want := []FanChannel{
		{Number: 2, RPM: 3550, PWM: 255, Enable: 1},
		{Number: 3, RPM: 1790, PWM: 143, Enable: 1},
	}
	for index := range want {
		want[index].RPMPath = got.Channels[index].RPMPath
		want[index].PWMPath = got.Channels[index].PWMPath
		want[index].EnablePath = got.Channels[index].EnablePath
	}
	if !reflect.DeepEqual(got.Channels, want) {
		t.Fatalf("channels = %+v; want %+v", got.Channels, want)
	}
}

func TestDiscoverFansRejectsMissingIT87Driver(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "class/hwmon/hwmon0/name", "coretemp\n", 0o444)
	_, err := DiscoverFans(root)
	if !errors.Is(err, ErrDriverUnavailable) {
		t.Fatalf("DiscoverFans error = %v; want ErrDriverUnavailable", err)
	}
}

func TestDiscoverDisksUsesASM1166LocalPortsAndIgnoresOtherControllers(t *testing.T) {
	root := t.TempDir()
	controller := filepath.Join(root, "devices/pci0000:00/0000:00:1c.2/0000:02:00.0")
	writeFixture(t, controller, "vendor", "0x1b21\n", 0o444)
	writeFixture(t, controller, "device", "0x1166\n", 0o444)
	for port := 6; port <= 11; port++ {
		if err := os.MkdirAll(filepath.Join(controller, "ata"+itoa(port)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	pciRoot := filepath.Join(root, "bus/pci/devices")
	if err := os.MkdirAll(pciRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(controller, filepath.Join(pciRoot, "0000:02:00.0")); err != nil {
		t.Fatal(err)
	}

	addDisk := func(name string, globalPort int, model string) {
		t.Helper()
		target := filepath.Join(controller, "ata"+itoa(globalPort), "host0", "target0:0:0", "0:0:0:0")
		writeFixture(t, target, "model", model+"\n", 0o444)
		block := filepath.Join(root, "block", name)
		if err := os.MkdirAll(block, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(block, "device")); err != nil {
			t.Fatal(err)
		}
	}
	addDisk("sda", 7, "disk-a")
	addDisk("sdb", 10, "disk-b")

	other := filepath.Join(root, "devices/pci0000:00/0000:00:17.0/ata1/disk")
	writeFixture(t, other, "model", "must-ignore\n", 0o444)
	otherBlock := filepath.Join(root, "block", "sdc")
	if err := os.MkdirAll(otherBlock, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, filepath.Join(otherBlock, "device")); err != nil {
		t.Fatal(err)
	}

	disks, err := DiscoverDisks(root)
	if err != nil {
		t.Fatalf("DiscoverDisks returned error: %v", err)
	}
	if len(disks) != 2 || disks[2].Device != "sda" || disks[2].GlobalATAPort != 7 || disks[5].Device != "sdb" {
		t.Fatalf("disks = %+v; want sda on local port 2 and sdb on local port 5", disks)
	}
}

func itoa(value int) string {
	const digits = "0123456789"
	if value < 10 {
		return string(digits[value])
	}
	return string(digits[value/10]) + string(digits[value%10])
}
