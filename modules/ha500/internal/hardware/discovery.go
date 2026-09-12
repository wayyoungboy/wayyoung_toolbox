package hardware

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	asm1166Vendor = "0x1b21"
	asm1166Device = "0x1166"
)

var (
	ErrDriverUnavailable = errors.New("it87 hwmon driver is unavailable")
	ErrASMUnavailable    = errors.New("exactly one ASM1166 controller is required")
	ataName              = regexp.MustCompile(`^ata([0-9]+)$`)
	ataPath              = regexp.MustCompile(`(?:^|/)ata([0-9]+)(?:/|$)`)
)

type Temperature struct {
	Source       string `json:"source"`
	Label        string `json:"label"`
	MilliCelsius int    `json:"milli_celsius"`
	InputPath    string `json:"-"`
}

type FanChannel struct {
	Number     int    `json:"number"`
	RPM        int    `json:"rpm"`
	PWM        int    `json:"pwm"`
	Enable     int    `json:"enable"`
	RPMPath    string `json:"-"`
	PWMPath    string `json:"-"`
	EnablePath string `json:"-"`
	Verified   bool   `json:"verified"`
}

type FanHardware struct {
	ChipName    string       `json:"chip_name"`
	Temperature Temperature  `json:"temperature"`
	Channels    []FanChannel `json:"channels"`
}

type Disk struct {
	Device         string `json:"device"`
	Model          string `json:"model"`
	ControllerPort int    `json:"controller_port"`
	GlobalATAPort  int    `json:"global_ata_port"`
}

func readText(path string) (string, error) {
	value, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(value)), nil
}

func readInt(path string) (int, error) {
	value, err := readText(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(value)
}

func DiscoverFans(sysRoot string) (FanHardware, error) {
	hwmonPaths, err := filepath.Glob(filepath.Join(sysRoot, "class", "hwmon", "hwmon*"))
	if err != nil {
		return FanHardware{}, fmt.Errorf("scan hwmon: %w", err)
	}
	var chipPath string
	var result FanHardware
	for _, path := range hwmonPaths {
		name, readErr := readText(filepath.Join(path, "name"))
		if readErr != nil {
			continue
		}
		if strings.HasPrefix(name, "it86") || strings.HasPrefix(name, "it87") {
			if chipPath != "" {
				return FanHardware{}, fmt.Errorf("%w: multiple matching chips", ErrDriverUnavailable)
			}
			chipPath = path
			result.ChipName = name
		}
		if name == "coretemp" {
			result.Temperature = discoverCPUPackage(path)
		}
	}
	if chipPath == "" {
		return FanHardware{}, ErrDriverUnavailable
	}
	for number := 1; number <= 8; number++ {
		rpmPath := filepath.Join(chipPath, fmt.Sprintf("fan%d_input", number))
		pwmPath := filepath.Join(chipPath, fmt.Sprintf("pwm%d", number))
		enablePath := filepath.Join(chipPath, fmt.Sprintf("pwm%d_enable", number))
		rpm, rpmErr := readInt(rpmPath)
		pwm, pwmErr := readInt(pwmPath)
		enable, enableErr := readInt(enablePath)
		if rpmErr != nil || pwmErr != nil || enableErr != nil || rpm <= 0 {
			continue
		}
		result.Channels = append(result.Channels, FanChannel{
			Number: number, RPM: rpm, PWM: pwm, Enable: enable,
			RPMPath: rpmPath, PWMPath: pwmPath, EnablePath: enablePath,
		})
	}
	return result, nil
}

func discoverCPUPackage(hwmonPath string) Temperature {
	for number := 1; number <= 32; number++ {
		labelPath := filepath.Join(hwmonPath, fmt.Sprintf("temp%d_label", number))
		inputPath := filepath.Join(hwmonPath, fmt.Sprintf("temp%d_input", number))
		label, err := readText(labelPath)
		if err != nil || (label != "Package id 0" && label != "CPU Package") {
			continue
		}
		value, err := readInt(inputPath)
		if err == nil {
			return Temperature{Source: "coretemp", Label: label, MilliCelsius: value, InputPath: inputPath}
		}
	}
	return Temperature{}
}

func DiscoverDisks(sysRoot string) (map[int]Disk, error) {
	controllers, err := findASMControllers(sysRoot)
	if err != nil {
		return nil, err
	}
	if len(controllers) != 1 {
		return nil, ErrASMUnavailable
	}
	controller := controllers[0]
	entries, err := os.ReadDir(controller)
	if err != nil {
		return nil, fmt.Errorf("read ASM1166 ports: %w", err)
	}
	globalPorts := make([]int, 0, 6)
	for _, entry := range entries {
		match := ataName.FindStringSubmatch(entry.Name())
		if len(match) != 2 || !entry.IsDir() {
			continue
		}
		port, parseErr := strconv.Atoi(match[1])
		if parseErr == nil {
			globalPorts = append(globalPorts, port)
		}
	}
	sort.Ints(globalPorts)
	globalToLocal := make(map[int]int, len(globalPorts))
	for index, global := range globalPorts {
		if index == 6 {
			break
		}
		globalToLocal[global] = index + 1
	}

	resolvedController, err := filepath.EvalSymlinks(controller)
	if err != nil {
		return nil, fmt.Errorf("resolve ASM1166 controller: %w", err)
	}
	blocks, err := os.ReadDir(filepath.Join(sysRoot, "block"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[int]Disk{}, nil
		}
		return nil, fmt.Errorf("read block devices: %w", err)
	}
	result := make(map[int]Disk)
	for _, block := range blocks {
		resolved, resolveErr := filepath.EvalSymlinks(filepath.Join(sysRoot, "block", block.Name(), "device"))
		if resolveErr != nil || !pathWithin(resolvedController, resolved) {
			continue
		}
		match := ataPath.FindStringSubmatch(filepath.ToSlash(resolved))
		if len(match) != 2 {
			continue
		}
		globalPort, parseErr := strconv.Atoi(match[1])
		if parseErr != nil {
			continue
		}
		localPort, ok := globalToLocal[globalPort]
		if !ok {
			continue
		}
		model, _ := readText(filepath.Join(resolved, "model"))
		result[localPort] = Disk{Device: block.Name(), Model: model, ControllerPort: localPort, GlobalATAPort: globalPort}
	}
	return result, nil
}

func findASMControllers(sysRoot string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(sysRoot, "bus", "pci", "devices"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read PCI devices: %w", err)
	}
	var result []string
	for _, entry := range entries {
		path := filepath.Join(sysRoot, "bus", "pci", "devices", entry.Name())
		vendor, _ := readText(filepath.Join(path, "vendor"))
		device, _ := readText(filepath.Join(path, "device"))
		if strings.EqualFold(vendor, asm1166Vendor) && strings.EqualFold(device, asm1166Device) {
			resolved, resolveErr := filepath.EvalSymlinks(path)
			if resolveErr == nil {
				result = append(result, resolved)
			}
		}
	}
	sort.Strings(result)
	return result, nil
}

func pathWithin(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
