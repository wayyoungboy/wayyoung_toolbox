package server

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strconv"
	"strings"

	"github.com/wayyoungboy/ha500-fnos-toolbox/internal/config"
	"github.com/wayyoungboy/ha500-fnos-toolbox/internal/control"
)

const maxRequestBody = 32 << 10

//go:embed public/*
var publicFiles embed.FS

type HardwareStatus struct {
	Supported bool   `json:"supported"`
	Match     string `json:"match"`
	ASM1166   bool   `json:"asm1166"`
	GPIO      bool   `json:"gpio"`
	FanChip   string `json:"fan_chip,omitempty"`
	FanDriver bool   `json:"fan_driver"`
	Conflict  string `json:"conflict,omitempty"`
}

type DiskStatus struct {
	Bay            int    `json:"bay"`
	ControllerPort int    `json:"controller_port"`
	Present        bool   `json:"present"`
	Device         string `json:"device,omitempty"`
	Model          string `json:"model,omitempty"`
}

type FanStatus struct {
	Number      int               `json:"number"`
	Name        string            `json:"name"`
	RPM         int               `json:"rpm"`
	PWM         int               `json:"pwm"`
	DutyPercent int               `json:"duty_percent"`
	Verified    bool              `json:"verified"`
	Policy      control.FanPolicy `json:"policy"`
}

type LEDControl struct {
	Mode   control.LEDMode `json:"mode"`
	States [5]bool         `json:"states"`
}

type TemperatureSourceStatus struct {
	ID           string   `json:"id"`
	Kind         string   `json:"kind"`
	Label        string   `json:"label"`
	TemperatureC *float64 `json:"temperature_c,omitempty"`
	Available    bool     `json:"available"`
}

type Status struct {
	Version            string                    `json:"version"`
	Running            bool                      `json:"running"`
	MonitorOnly        bool                      `json:"monitor_only"`
	Hardware           HardwareStatus            `json:"hardware"`
	Disks              []DiskStatus              `json:"disks"`
	Fans               []FanStatus               `json:"fans"`
	Temperature        float64                   `json:"temperature_c"`
	TemperatureSources []TemperatureSourceStatus `json:"temperature_sources"`
	LED                LEDControl                `json:"led_control"`
	LastError          string                    `json:"last_error,omitempty"`
	UpdatedAt          string                    `json:"updated_at"`
}

type LogItem struct {
	Timestamp string `json:"timestamp"`
	Level     string `json:"level"`
	Message   string `json:"message"`
}

type LogFile struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

type LogSnapshot struct {
	Items       []LogItem `json:"items"`
	Files       []LogFile `json:"files"`
	Limit       int       `json:"limit"`
	MaxBytes    int64     `json:"max_bytes_per_file"`
	BackupCount int       `json:"backup_count"`
}

type Backend interface {
	Snapshot(context.Context) (Status, error)
	Settings() config.Settings
	LEDControl() LEDControl
	SetLEDControl(context.Context, LEDControl) error
	SetBayPortMap(context.Context, [5]int) error
	SetFanPolicy(context.Context, int, control.FanPolicy) error
	VerifyFan(context.Context, int, bool) (control.Verification, error)
	ReadLogs(int) (LogSnapshot, error)
	ClearLogs() error
}

func NewHandler(gatewayPrefix string, backend Backend) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", admin(func(writer http.ResponseWriter, request *http.Request) error {
		status, err := backend.Snapshot(request.Context())
		if err != nil {
			return err
		}
		return writeJSON(writer, http.StatusOK, status)
	}))
	mux.HandleFunc("GET /api/settings", admin(func(writer http.ResponseWriter, _ *http.Request) error {
		return writeJSON(writer, http.StatusOK, map[string]any{"settings": backend.Settings(), "led_control": backend.LEDControl()})
	}))
	mux.HandleFunc("PUT /api/led", admin(func(writer http.ResponseWriter, request *http.Request) error {
		var value LEDControl
		if err := decodeJSON(writer, request, &value); err != nil {
			return badRequest(err)
		}
		if value.Mode != control.LEDAutomatic && value.Mode != control.LEDManual {
			return badRequest(errors.New("mode must be auto or manual"))
		}
		if err := backend.SetLEDControl(request.Context(), value); err != nil {
			return err
		}
		return writeJSON(writer, http.StatusOK, value)
	}))
	mux.HandleFunc("PUT /api/led/mapping", admin(func(writer http.ResponseWriter, request *http.Request) error {
		var payload struct {
			BayPortMap []int `json:"bay_port_map"`
		}
		if err := decodeJSON(writer, request, &payload); err != nil {
			return badRequest(err)
		}
		mapping, err := control.ValidateBayPortMap(payload.BayPortMap)
		if err != nil {
			return badRequest(err)
		}
		if err := backend.SetBayPortMap(request.Context(), mapping); err != nil {
			return err
		}
		return writeJSON(writer, http.StatusOK, map[string]any{"bay_port_map": mapping})
	}))
	mux.HandleFunc("PUT /api/fans/{channel}/policy", admin(func(writer http.ResponseWriter, request *http.Request) error {
		channel, err := strconv.Atoi(request.PathValue("channel"))
		if err != nil || channel < 1 || channel > 8 {
			return badRequest(errors.New("invalid fan channel"))
		}
		var policy control.FanPolicy
		if err := decodeJSON(writer, request, &policy); err != nil {
			return badRequest(err)
		}
		if err := policy.Validate(); err != nil {
			return badRequest(err)
		}
		if err := backend.SetFanPolicy(request.Context(), channel, policy); err != nil {
			return err
		}
		return writeJSON(writer, http.StatusOK, policy)
	}))
	mux.HandleFunc("POST /api/fans/{channel}/verify", admin(func(writer http.ResponseWriter, request *http.Request) error {
		channel, err := strconv.Atoi(request.PathValue("channel"))
		if err != nil || channel < 1 || channel > 8 {
			return badRequest(errors.New("invalid fan channel"))
		}
		var payload struct {
			AllowLowering bool `json:"allow_lowering_at_full"`
		}
		if request.ContentLength != 0 {
			if err := decodeJSON(writer, request, &payload); err != nil {
				return badRequest(err)
			}
		}
		result, err := backend.VerifyFan(request.Context(), channel, payload.AllowLowering)
		if err != nil {
			if errors.Is(err, control.ErrLoweringConsentRequired) {
				return apiError{status: http.StatusConflict, err: err}
			}
			return err
		}
		return writeJSON(writer, http.StatusOK, result)
	}))
	mux.HandleFunc("GET /api/logs", admin(func(writer http.ResponseWriter, request *http.Request) error {
		limit := 500
		if raw := request.URL.Query().Get("limit"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil {
				return badRequest(errors.New("invalid log limit"))
			}
			limit = parsed
		}
		logs, err := backend.ReadLogs(limit)
		if err != nil {
			return err
		}
		return writeJSON(writer, http.StatusOK, logs)
	}))
	mux.HandleFunc("DELETE /api/logs", admin(func(writer http.ResponseWriter, _ *http.Request) error {
		if err := backend.ClearLogs(); err != nil {
			return err
		}
		return writeJSON(writer, http.StatusOK, map[string]bool{"cleared": true})
	}))

	staticRoot, err := fs.Sub(publicFiles, "public")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(staticRoot))
	mux.HandleFunc("GET /", func(writer http.ResponseWriter, request *http.Request) {
		path := strings.TrimPrefix(request.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		if _, err := fs.Stat(staticRoot, path); err != nil {
			request.URL.Path = "/"
		}
		writer.Header().Set("Cache-Control", "no-store")
		fileServer.ServeHTTP(writer, request)
	})

	prefix := strings.TrimSuffix(gatewayPrefix, "/")
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if prefix != "" && request.URL.Path == prefix {
			http.Redirect(writer, request, prefix+"/", http.StatusPermanentRedirect)
			return
		}
		if prefix != "" && strings.HasPrefix(request.URL.Path, prefix+"/") {
			clone := request.Clone(request.Context())
			clone.URL.Path = strings.TrimPrefix(request.URL.Path, prefix)
			if clone.URL.Path == "" {
				clone.URL.Path = "/"
			}
			mux.ServeHTTP(writer, clone)
			return
		}
		mux.ServeHTTP(writer, request)
	})
}

type handlerFunc func(http.ResponseWriter, *http.Request) error

func admin(next handlerFunc) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if !strings.EqualFold(request.Header.Get("X-Trim-Isadmin"), "true") {
			_ = writeJSON(writer, http.StatusForbidden, map[string]string{"error": "需要 fnOS 管理员权限"})
			return
		}
		if err := next(writer, request); err != nil {
			var api apiError
			if errors.As(err, &api) {
				_ = writeJSON(writer, api.status, map[string]string{"error": api.err.Error()})
				return
			}
			_ = writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
	}
}

type apiError struct {
	status int
	err    error
}

func (err apiError) Error() string { return err.err.Error() }
func (err apiError) Unwrap() error { return err.err }
func badRequest(err error) error   { return apiError{status: http.StatusBadRequest, err: err} }

func decodeJSON(writer http.ResponseWriter, request *http.Request, destination any) error {
	request.Body = http.MaxBytesReader(writer, request.Body, maxRequestBody)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("请求 JSON 无效: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("请求只能包含一个 JSON 对象")
	}
	return nil
}

func writeJSON(writer http.ResponseWriter, status int, value any) error {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	return json.NewEncoder(writer).Encode(value)
}
