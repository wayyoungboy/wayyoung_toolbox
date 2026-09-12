package hardware

import (
	"os"
	"path/filepath"
	"testing"
)

func makeReferenceFingerprint(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, field := range []string{"sys_vendor", "product_name", "board_vendor", "board_name"} {
		writeFixture(t, root, filepath.Join("class/dmi/id", field), "Default string\n", 0o444)
	}
	writeFixture(t, root, "class/dmi/id/chassis_type", "32\n", 0o444)
	writeFixture(t, root, "devices/pci0000:00/0000:02:00.0/vendor", "0x1b21\n", 0o444)
	writeFixture(t, root, "devices/pci0000:00/0000:02:00.0/device", "0x1166\n", 0o444)
	pciRoot := filepath.Join(root, "bus/pci/devices")
	if err := os.MkdirAll(pciRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "devices/pci0000:00/0000:02:00.0"), filepath.Join(pciRoot, "0000:02:00.0")); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, root, "class/gpio/gpiochip512/label", "INT34C8:00\n", 0o444)
	writeFixture(t, root, "class/gpio/gpiochip512/base", "512\n", 0o444)
	writeFixture(t, root, "class/gpio/gpiochip512/ngpio", "300\n", 0o444)
	writeFixture(t, root, "proc/cpuinfo", "model name\t: Intel(R) Celeron(R) N5105 @ 2.00GHz\n", 0o444)
	return root
}

func TestDetectHA500AcceptsConservativeReferenceFingerprint(t *testing.T) {
	root := makeReferenceFingerprint(t)
	report := DetectHA500(root, filepath.Join(root, "proc/cpuinfo"))
	if !report.Supported || report.Match != "ha500-reference-fingerprint" {
		t.Fatalf("report = %+v; want supported reference fingerprint", report)
	}
	if report.GPIOBase != 512 || report.ASM1166Count != 1 {
		t.Fatalf("report = %+v; want GPIO base 512 and one ASM1166", report)
	}
}

func TestDetectHA500FailsClosedWhenASMorGPIOIsMissing(t *testing.T) {
	root := makeReferenceFingerprint(t)
	if err := os.Remove(filepath.Join(root, "bus/pci/devices/0000:02:00.0")); err != nil {
		t.Fatal(err)
	}
	report := DetectHA500(root, filepath.Join(root, "proc/cpuinfo"))
	if report.Supported {
		t.Fatalf("unsupported hardware was accepted: %+v", report)
	}
	root = makeReferenceFingerprint(t)
	if err := os.Remove(filepath.Join(root, "class/gpio/gpiochip512/label")); err != nil {
		t.Fatal(err)
	}
	report = DetectHA500(root, filepath.Join(root, "proc/cpuinfo"))
	if report.Supported {
		t.Fatalf("hardware without PCH GPIO was accepted: %+v", report)
	}
}

func TestSysfsGPIOWritesActiveLineWithoutEscapingChipRange(t *testing.T) {
	root := makeReferenceFingerprint(t)
	writeFixture(t, root, "class/gpio/gpio587/direction", "out\n", 0o644)
	writeFixture(t, root, "class/gpio/gpio587/value", "1\n", 0o644)
	writer, err := NewSysfsGPIO(root, "INT34C8:00")
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Write(75, 0); err != nil {
		t.Fatal(err)
	}
	value, err := os.ReadFile(filepath.Join(root, "class/gpio/gpio587/value"))
	if err != nil {
		t.Fatal(err)
	}
	if string(value) != "0\n" {
		t.Fatalf("GPIO value = %q; want active-low 0", value)
	}
	if err := writer.Write(301, 0); err == nil {
		t.Fatal("out-of-range GPIO offset was accepted")
	}
}
