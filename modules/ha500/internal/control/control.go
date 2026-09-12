package control

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"

	"github.com/wayyoungboy/ha500-fnos-toolbox/internal/hardware"
)

type LEDMode string

const (
	LEDAutomatic LEDMode = "auto"
	LEDManual    LEDMode = "manual"
)

var defaultLEDOffsets = [5]int{75, 261, 100, 260, 256}

type GPIOWriter interface {
	Write(offset, value int) error
}

type LEDController struct {
	writer  GPIOWriter
	offsets [5]int
}

func NewLEDController(writer GPIOWriter) *LEDController {
	return &LEDController{writer: writer, offsets: defaultLEDOffsets}
}

func (controller *LEDController) Apply(mode LEDMode, manual, present [5]bool) error {
	states := present
	if mode == LEDManual {
		states = manual
	} else if mode != LEDAutomatic {
		return fmt.Errorf("unknown LED mode %q", mode)
	}
	for index, offset := range controller.offsets {
		value := 1
		if states[index] {
			value = 0
		}
		if err := controller.writer.Write(offset, value); err != nil {
			return fmt.Errorf("write bay %d LED: %w", index+1, err)
		}
	}
	return nil
}

func ValidateBayPortMap(values []int) ([5]int, error) {
	var result [5]int
	if len(values) != len(result) {
		return result, errors.New("five bay ports are required")
	}
	seen := make(map[int]bool, len(result))
	for index, value := range values {
		if value < 1 || value > 6 || seen[value] {
			return [5]int{}, errors.New("bay ports must be distinct values from 1 to 6")
		}
		seen[value] = true
		result[index] = value
	}
	return result, nil
}

type FanMode string

const (
	ModeMonitor   FanMode = "monitor"
	ModeCurve     FanMode = "curve"
	ModeFixed     FanMode = "fixed"
	ModeFullSpeed FanMode = "full"
)

var (
	ErrLoweringConsentRequired = errors.New("lowering a full-speed fan requires explicit consent")
	ErrTemperatureUnavailable  = errors.New("temperature source is unavailable")
	ErrFanStopped              = errors.New("controlled fan has no RPM feedback")
)

type CurvePoint struct {
	TemperatureC float64 `json:"temperature_c"`
	DutyPercent  int     `json:"duty_percent"`
}

type Curve []CurvePoint

func (curve Curve) Duty(temperature float64) int {
	if len(curve) == 0 {
		return 100
	}
	if temperature <= curve[0].TemperatureC {
		return curve[0].DutyPercent
	}
	for index := 1; index < len(curve); index++ {
		upper := curve[index]
		lower := curve[index-1]
		if temperature <= upper.TemperatureC {
			ratio := (temperature - lower.TemperatureC) / (upper.TemperatureC - lower.TemperatureC)
			return int(math.Round(float64(lower.DutyPercent) + ratio*float64(upper.DutyPercent-lower.DutyPercent)))
		}
	}
	return curve[len(curve)-1].DutyPercent
}

type FanPolicy struct {
	Mode                 FanMode `json:"mode"`
	TemperatureSource    string  `json:"temperature_source,omitempty"`
	MinDutyPercent       int     `json:"min_duty_percent,omitempty"`
	FixedDutyPercent     int     `json:"fixed_duty_percent,omitempty"`
	CriticalTemperatureC int     `json:"critical_temperature_c,omitempty"`
	Curve                Curve   `json:"curve,omitempty"`
}

func (policy FanPolicy) Validate() error {
	switch policy.Mode {
	case ModeMonitor, ModeFullSpeed:
		return nil
	case ModeFixed, ModeCurve:
	default:
		return fmt.Errorf("unsupported fan mode %q", policy.Mode)
	}
	if !validTemperatureSource(policy.TemperatureSource) {
		return errors.New("temperature source must be CPU Package or a disk fingerprint")
	}
	if policy.MinDutyPercent < 20 || policy.MinDutyPercent > 100 {
		return errors.New("minimum duty must be between 20 and 100 percent")
	}
	if policy.CriticalTemperatureC < 50 || policy.CriticalTemperatureC > 105 {
		return errors.New("critical temperature must be between 50 and 105 C")
	}
	if policy.Mode == ModeFixed {
		if policy.FixedDutyPercent < policy.MinDutyPercent || policy.FixedDutyPercent > 100 {
			return errors.New("fixed duty must be at least the safety floor and at most 100 percent")
		}
		return nil
	}
	if len(policy.Curve) < 2 {
		return errors.New("automatic curve requires at least two points")
	}
	for index, point := range policy.Curve {
		if point.DutyPercent < policy.MinDutyPercent || point.DutyPercent > 100 {
			return errors.New("curve duty is outside the safe range")
		}
		if index > 0 {
			previous := policy.Curve[index-1]
			if point.TemperatureC <= previous.TemperatureC || point.DutyPercent < previous.DutyPercent {
				return errors.New("curve points must increase by temperature and duty")
			}
		}
	}
	last := policy.Curve[len(policy.Curve)-1]
	if last.TemperatureC > float64(policy.CriticalTemperatureC) || last.DutyPercent != 100 {
		return errors.New("curve must reach 100 percent no later than the critical temperature")
	}
	return nil
}

func validTemperatureSource(source string) bool {
	if source == "cpu" {
		return true
	}
	const prefix = "disk:"
	if len(source) != len(prefix)+sha256HexLength || source[:len(prefix)] != prefix {
		return false
	}
	_, err := hex.DecodeString(source[len(prefix):])
	return err == nil
}

const sha256HexLength = 64

type PolicySet map[int]FanPolicy

func (policies PolicySet) Validate(available []int) error {
	known := make(map[int]bool, len(available))
	for _, channel := range available {
		known[channel] = true
	}
	for channel, policy := range policies {
		if !known[channel] {
			return fmt.Errorf("fan channel %d is unavailable", channel)
		}
		if err := policy.Validate(); err != nil {
			return fmt.Errorf("fan channel %d: %w", channel, err)
		}
	}
	return nil
}

func (policies PolicySet) Duties(temperatures map[string]float64) (map[int]int, error) {
	result := make(map[int]int, len(policies))
	for channel, policy := range policies {
		if err := policy.Validate(); err != nil {
			return nil, fmt.Errorf("fan channel %d: %w", channel, err)
		}
		switch policy.Mode {
		case ModeMonitor:
			continue
		case ModeFullSpeed:
			result[channel] = 100
			continue
		}
		temperature, ok := temperatures[policy.TemperatureSource]
		if !ok {
			return nil, ErrTemperatureUnavailable
		}
		if temperature >= float64(policy.CriticalTemperatureC) {
			result[channel] = 100
		} else if policy.Mode == ModeFixed {
			result[channel] = policy.FixedDutyPercent
		} else {
			result[channel] = max(policy.MinDutyPercent, policy.Curve.Duty(temperature))
		}
	}
	return result, nil
}

type SensorIO interface {
	ReadInt(path string) (int, error)
	WriteInt(path string, value int) error
}

type TemperatureReader interface {
	ReadTemperature(string) (float64, error)
}

type pathTemperatureReader struct {
	sensors SensorIO
	paths   map[string]string
}

func (reader pathTemperatureReader) ReadTemperature(source string) (float64, error) {
	path, ok := reader.paths[source]
	if !ok {
		return 0, ErrTemperatureUnavailable
	}
	value, err := reader.sensors.ReadInt(path)
	if err != nil {
		return 0, err
	}
	return float64(value) / 1000, nil
}

type VerifyOptions struct {
	AllowLoweringAtFull bool
	StepPercent         int
	Settle              func(context.Context) error
}

type Verification struct {
	Supported   bool `json:"supported"`
	InitialRPM  int  `json:"initial_rpm"`
	ObservedRPM int  `json:"observed_rpm"`
}

func VerifySoftwareControl(ctx context.Context, sensors SensorIO, channel hardware.FanChannel, options VerifyOptions) (result Verification, err error) {
	initialRPM, err := sensors.ReadInt(channel.RPMPath)
	if err != nil {
		return result, fmt.Errorf("read initial RPM: %w", err)
	}
	initialPWM, err := sensors.ReadInt(channel.PWMPath)
	if err != nil {
		return result, fmt.Errorf("read initial PWM: %w", err)
	}
	initialEnable, err := sensors.ReadInt(channel.EnablePath)
	if err != nil {
		return result, fmt.Errorf("read initial PWM mode: %w", err)
	}
	if initialPWM >= 255 && !options.AllowLoweringAtFull {
		return result, ErrLoweringConsentRequired
	}
	step := options.StepPercent
	if step == 0 {
		step = 12
	}
	target := min(255, initialPWM+percentToPWM(step))
	if initialPWM >= 255 {
		target = max(0, initialPWM-percentToPWM(step))
	}
	restored := false
	defer func() {
		if restored {
			return
		}
		restoreErr := errors.Join(
			sensors.WriteInt(channel.PWMPath, initialPWM),
			sensors.WriteInt(channel.EnablePath, initialEnable),
		)
		if restoreErr != nil {
			err = errors.Join(err, fmt.Errorf("restore fan state: %w", restoreErr))
		}
	}()
	if err = sensors.WriteInt(channel.EnablePath, 1); err != nil {
		return result, fmt.Errorf("enable manual PWM: %w", err)
	}
	if err = sensors.WriteInt(channel.PWMPath, target); err != nil {
		return result, fmt.Errorf("write verification PWM: %w", err)
	}
	if options.Settle != nil {
		if err = options.Settle(ctx); err != nil {
			return result, fmt.Errorf("wait for RPM response: %w", err)
		}
	}
	observedRPM, err := sensors.ReadInt(channel.RPMPath)
	if err != nil {
		return result, fmt.Errorf("read observed RPM: %w", err)
	}
	threshold := max(100, initialRPM/20)
	result = Verification{InitialRPM: initialRPM, ObservedRPM: observedRPM, Supported: abs(observedRPM-initialRPM) >= threshold}
	return result, nil
}

type FanController struct {
	sensors      SensorIO
	channels     map[int]hardware.FanChannel
	temperatures TemperatureReader
}

func NewFanController(sensors SensorIO, channels []hardware.FanChannel, temperatures map[string]string) *FanController {
	return NewFanControllerWithTemperatureReader(sensors, channels, pathTemperatureReader{sensors: sensors, paths: temperatures})
}

func NewFanControllerWithTemperatureReader(sensors SensorIO, channels []hardware.FanChannel, temperatures TemperatureReader) *FanController {
	indexed := make(map[int]hardware.FanChannel, len(channels))
	for _, channel := range channels {
		indexed[channel.Number] = channel
	}
	return &FanController{sensors: sensors, channels: indexed, temperatures: temperatures}
}

func (controller *FanController) Apply(_ context.Context, policies PolicySet) error {
	available := make([]int, 0, len(controller.channels))
	for number := range controller.channels {
		available = append(available, number)
	}
	sort.Ints(available)
	if err := policies.Validate(available); err != nil {
		return err
	}
	owned := controller.ownedChannels(policies)
	for _, channel := range owned {
		rpm, err := controller.sensors.ReadInt(channel.RPMPath)
		if err != nil || rpm <= 0 {
			_ = controller.forceFull(owned)
			return ErrFanStopped
		}
	}
	temperatures := make(map[string]float64)
	for _, policy := range policies {
		if policy.Mode == ModeMonitor || policy.Mode == ModeFullSpeed {
			continue
		}
		if _, exists := temperatures[policy.TemperatureSource]; exists {
			continue
		}
		if controller.temperatures == nil {
			_ = controller.forceFull(owned)
			return ErrTemperatureUnavailable
		}
		value, err := controller.temperatures.ReadTemperature(policy.TemperatureSource)
		if err != nil {
			_ = controller.forceFull(owned)
			return ErrTemperatureUnavailable
		}
		temperatures[policy.TemperatureSource] = value
	}
	duties, err := policies.Duties(temperatures)
	if err != nil {
		_ = controller.forceFull(owned)
		return err
	}
	for number, duty := range duties {
		channel := controller.channels[number]
		if !channel.Verified {
			_ = controller.forceFull(owned)
			return fmt.Errorf("fan channel %d has not passed software-control verification", number)
		}
		if err := controller.sensors.WriteInt(channel.EnablePath, 1); err != nil {
			_ = controller.forceFull(owned)
			return err
		}
		if err := controller.sensors.WriteInt(channel.PWMPath, percentToPWM(duty)); err != nil {
			_ = controller.forceFull(owned)
			return err
		}
	}
	return nil
}

func (controller *FanController) ownedChannels(policies PolicySet) []hardware.FanChannel {
	result := make([]hardware.FanChannel, 0, len(policies))
	for number, policy := range policies {
		channel, ok := controller.channels[number]
		if ok && channel.Verified && policy.Mode != ModeMonitor {
			result = append(result, channel)
		}
	}
	return result
}

func (controller *FanController) forceFull(channels []hardware.FanChannel) error {
	var result error
	for _, channel := range channels {
		result = errors.Join(result,
			controller.sensors.WriteInt(channel.EnablePath, 1),
			controller.sensors.WriteInt(channel.PWMPath, 255),
		)
	}
	return result
}

func (controller *FanController) FailSafe(policies PolicySet) error {
	return controller.forceFull(controller.ownedChannels(policies))
}

func percentToPWM(percent int) int {
	return min(255, max(0, (percent*255+50)/100))
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
