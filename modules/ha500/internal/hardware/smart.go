package hardware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	smartctlOutputLimit = 1 << 20
	smartctlTimeout     = 5 * time.Second
)

var sataBlockDevice = regexp.MustCompile(`^sd[a-z]+$`)

type SMARTIdentity struct {
	Serial       string
	Model        string
	TemperatureC *float64
}

type SMARTReader interface {
	Read(context.Context, string) (SMARTIdentity, error)
}

type SmartctlReader struct {
	executable  string
	timeout     time.Duration
	outputLimit int
	errorLimit  int
}

func NewSmartctlReader(executable string) *SmartctlReader {
	return NewSmartctlReaderWithOptions(executable, SmartctlReaderOptions{})
}

type SmartctlReaderOptions struct {
	Timeout     time.Duration
	OutputLimit int
	ErrorLimit  int
}

func NewSmartctlReaderWithOptions(executable string, options SmartctlReaderOptions) *SmartctlReader {
	if options.Timeout <= 0 {
		options.Timeout = smartctlTimeout
	}
	if options.OutputLimit <= 0 {
		options.OutputLimit = smartctlOutputLimit
	}
	if options.ErrorLimit <= 0 {
		options.ErrorLimit = 8 << 10
	}
	return &SmartctlReader{executable: executable, timeout: options.Timeout, outputLimit: options.OutputLimit, errorLimit: options.ErrorLimit}
}

type cappedBuffer struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (buffer *cappedBuffer) Write(value []byte) (int, error) {
	original := len(value)
	remaining := buffer.limit - buffer.buffer.Len()
	if remaining <= 0 {
		buffer.truncated = true
		return original, nil
	}
	if len(value) > remaining {
		value = value[:remaining]
		buffer.truncated = true
	}
	_, _ = buffer.buffer.Write(value)
	return original, nil
}

func (reader *SmartctlReader) Read(ctx context.Context, device string) (SMARTIdentity, error) {
	if !sataBlockDevice.MatchString(device) {
		return SMARTIdentity{}, fmt.Errorf("invalid SATA block device %q", device)
	}
	if reader.executable == "" {
		return SMARTIdentity{}, errors.New("smartctl executable is not configured")
	}
	commandContext, cancel := context.WithTimeout(ctx, reader.timeout)
	defer cancel()
	command := exec.CommandContext(commandContext, reader.executable, "-d", "sat", "-n", "standby,0", "-j", "-a", "/dev/"+device)
	command.Env = append(command.Environ(), "LC_ALL=C")
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return nil
		}
		err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	command.WaitDelay = time.Second
	stdout := &cappedBuffer{limit: reader.outputLimit}
	stderr := &cappedBuffer{limit: reader.errorLimit}
	command.Stdout = stdout
	command.Stderr = stderr
	runErr := command.Run()
	if commandContext.Err() != nil {
		return SMARTIdentity{}, fmt.Errorf("smartctl %s: %w", device, commandContext.Err())
	}
	if stdout.truncated {
		return SMARTIdentity{}, fmt.Errorf("smartctl %s JSON exceeds %d bytes", device, reader.outputLimit)
	}

	var payload struct {
		Serial      string `json:"serial_number"`
		Model       string `json:"model_name"`
		Temperature *struct {
			Current json.Number `json:"current"`
		} `json:"temperature"`
	}
	decoder := json.NewDecoder(bytes.NewReader(stdout.buffer.Bytes()))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		if runErr != nil {
			return SMARTIdentity{}, fmt.Errorf("smartctl %s failed: %w", device, runErr)
		}
		return SMARTIdentity{}, fmt.Errorf("decode smartctl %s JSON: %w", device, err)
	}
	if runErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) || exitErr.ExitCode() < 0 || exitErr.ExitCode()&0x07 != 0 {
			return SMARTIdentity{}, fmt.Errorf("smartctl %s reported an operational failure: %w", device, runErr)
		}
	}
	payload.Serial = strings.TrimSpace(payload.Serial)
	if payload.Serial == "" {
		return SMARTIdentity{}, fmt.Errorf("smartctl %s returned no disk serial", device)
	}
	identity := SMARTIdentity{Serial: payload.Serial, Model: strings.TrimSpace(payload.Model)}
	if payload.Temperature != nil && payload.Temperature.Current != "" {
		value, err := payload.Temperature.Current.Float64()
		if err == nil && value >= 0 && value <= 120 {
			identity.TemperatureC = &value
		}
	}
	return identity, nil
}

type DiskTemperature struct {
	SourceID       string
	DisplayID      string
	Device         string
	Model          string
	ControllerPort int
	TemperatureC   float64
	Available      bool
}

func DiskTemperatureSourceID(serial string) string {
	fingerprint := sha256.Sum256([]byte(strings.TrimSpace(serial)))
	return "disk:" + hex.EncodeToString(fingerprint[:])
}

func diskTemperatureDisplayID(sourceID string) string {
	fingerprint := strings.TrimPrefix(sourceID, "disk:")
	if len(fingerprint) < 6 {
		return strings.ToUpper(fingerprint)
	}
	return strings.ToUpper(fingerprint[:6])
}

type SMARTMonitor struct {
	mu              sync.Mutex
	reader          SMARTReader
	refreshInterval time.Duration
	lastRefresh     time.Time
	cached          []DiskTemperature
	cachedErr       error
}

func NewSMARTMonitor(reader SMARTReader, refreshInterval time.Duration) *SMARTMonitor {
	return &SMARTMonitor{reader: reader, refreshInterval: refreshInterval}
}

func (monitor *SMARTMonitor) Snapshot(ctx context.Context, disks map[int]Disk) ([]DiskTemperature, error) {
	monitor.mu.Lock()
	defer monitor.mu.Unlock()
	now := time.Now()
	if monitor.refreshInterval > 0 && !monitor.lastRefresh.IsZero() && now.Sub(monitor.lastRefresh) < monitor.refreshInterval {
		return append([]DiskTemperature(nil), monitor.cached...), monitor.cachedErr
	}
	monitor.lastRefresh = now
	if len(disks) == 0 {
		monitor.cached = nil
		monitor.cachedErr = nil
		return nil, nil
	}

	ports := make([]int, 0, len(disks))
	for port := range disks {
		ports = append(ports, port)
	}
	sort.Ints(ports)
	type outcome struct {
		index    int
		identity SMARTIdentity
		err      error
	}
	outcomes := make(chan outcome, len(ports))
	for index, port := range ports {
		disk := disks[port]
		go func(index int, disk Disk) {
			identity, err := monitor.reader.Read(ctx, disk.Device)
			outcomes <- outcome{index: index, identity: identity, err: err}
		}(index, disk)
	}
	identities := make([]SMARTIdentity, len(ports))
	errorsByIndex := make([]error, len(ports))
	for range ports {
		result := <-outcomes
		identities[result.index] = result.identity
		errorsByIndex[result.index] = result.err
	}
	close(outcomes)

	items := make([]DiskTemperature, 0, len(ports))
	var resultErr error
	for index, port := range ports {
		disk := disks[port]
		if errorsByIndex[index] != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("%s: %w", disk.Device, errorsByIndex[index]))
			continue
		}
		identity := identities[index]
		sourceID := DiskTemperatureSourceID(identity.Serial)
		model := identity.Model
		if model == "" {
			model = disk.Model
		}
		item := DiskTemperature{
			SourceID: sourceID, DisplayID: diskTemperatureDisplayID(sourceID), Device: disk.Device,
			Model: model, ControllerPort: disk.ControllerPort,
		}
		if identity.TemperatureC != nil {
			item.TemperatureC = *identity.TemperatureC
			item.Available = true
		}
		items = append(items, item)
	}
	sort.Slice(items, func(left, right int) bool {
		if items[left].ControllerPort != items[right].ControllerPort {
			return items[left].ControllerPort < items[right].ControllerPort
		}
		return items[left].SourceID < items[right].SourceID
	})
	monitor.cached = append([]DiskTemperature(nil), items...)
	monitor.cachedErr = resultErr
	return append([]DiskTemperature(nil), items...), resultErr
}
