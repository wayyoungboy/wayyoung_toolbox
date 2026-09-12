package hardware

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const PCHGPIOLabel = "INT34C8:00"

type HA500Report struct {
	Supported    bool              `json:"supported"`
	Match        string            `json:"match"`
	DMI          map[string]string `json:"dmi"`
	CPUN5105     bool              `json:"cpu_n5105"`
	ASM1166Count int               `json:"asm1166_count"`
	ASM1166Path  string            `json:"asm1166_path,omitempty"`
	GPIOFound    bool              `json:"gpio_controller"`
	GPIOBase     int               `json:"gpio_base,omitempty"`
}

func DetectHA500(sysRoot, cpuInfoPath string) HA500Report {
	report := HA500Report{DMI: make(map[string]string)}
	fields := []string{"sys_vendor", "product_name", "board_vendor", "board_name"}
	genericDMI := true
	explicitHA500 := false
	for _, field := range fields {
		value, _ := readText(filepath.Join(sysRoot, "class", "dmi", "id", field))
		report.DMI[field] = value
		if strings.Contains(strings.ToUpper(value), "HA500") {
			explicitHA500 = true
		}
		if value != "" && value != "Default string" {
			genericDMI = false
		}
	}
	cpuInfo, _ := os.ReadFile(cpuInfoPath)
	report.CPUN5105 = strings.Contains(strings.ToUpper(string(cpuInfo)), "N5105")
	controllers, _ := findASMControllers(sysRoot)
	report.ASM1166Count = len(controllers)
	if len(controllers) == 1 {
		report.ASM1166Path = filepath.Base(controllers[0])
	}
	if gpio, err := findGPIOChip(sysRoot, PCHGPIOLabel); err == nil {
		report.GPIOFound = true
		report.GPIOBase = gpio.base
	}
	chassisType, _ := readText(filepath.Join(sysRoot, "class", "dmi", "id", "chassis_type"))
	baseMatch := report.CPUN5105 && report.ASM1166Count == 1 && report.GPIOFound
	switch {
	case explicitHA500 && baseMatch:
		report.Supported = true
		report.Match = "ha500-dmi"
	case genericDMI && chassisType == "32" && baseMatch:
		report.Supported = true
		report.Match = "ha500-reference-fingerprint"
	default:
		report.Match = "unsupported"
	}
	return report
}

type gpioChip struct {
	base  int
	count int
}

func findGPIOChip(sysRoot, label string) (gpioChip, error) {
	paths, err := filepath.Glob(filepath.Join(sysRoot, "class", "gpio", "gpiochip*"))
	if err != nil {
		return gpioChip{}, err
	}
	for _, path := range paths {
		value, readErr := readText(filepath.Join(path, "label"))
		if readErr != nil || value != label {
			continue
		}
		base, baseErr := readInt(filepath.Join(path, "base"))
		count, countErr := readInt(filepath.Join(path, "ngpio"))
		if baseErr != nil || countErr != nil || base < 0 || count <= 0 {
			return gpioChip{}, errors.New("GPIO chip has invalid base or range")
		}
		return gpioChip{base: base, count: count}, nil
	}
	return gpioChip{}, errors.New("GPIO controller not found")
}

type SysfsGPIO struct {
	root string
	chip gpioChip
}

func NewSysfsGPIO(sysRoot, label string) (*SysfsGPIO, error) {
	chip, err := findGPIOChip(sysRoot, label)
	if err != nil {
		return nil, err
	}
	return &SysfsGPIO{root: filepath.Join(sysRoot, "class", "gpio"), chip: chip}, nil
}

func (gpio *SysfsGPIO) Write(offset, value int) error {
	if offset < 0 || offset >= gpio.chip.count {
		return fmt.Errorf("GPIO offset %d is outside controller range", offset)
	}
	if value != 0 && value != 1 {
		return errors.New("GPIO value must be 0 or 1")
	}
	number := gpio.chip.base + offset
	linePath := filepath.Join(gpio.root, "gpio"+strconv.Itoa(number))
	if _, err := os.Stat(linePath); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(filepath.Join(gpio.root, "export"), []byte(strconv.Itoa(number)+"\n"), 0o200); err != nil && !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("export GPIO %d: %w", number, err)
		}
		for attempt := 0; attempt < 20; attempt++ {
			if _, err := os.Stat(linePath); err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	if err := os.WriteFile(filepath.Join(linePath, "direction"), []byte("out\n"), 0o600); err != nil {
		return fmt.Errorf("set GPIO %d direction: %w", number, err)
	}
	if err := os.WriteFile(filepath.Join(linePath, "value"), []byte(strconv.Itoa(value)+"\n"), 0o600); err != nil {
		return fmt.Errorf("write GPIO %d: %w", number, err)
	}
	return nil
}
