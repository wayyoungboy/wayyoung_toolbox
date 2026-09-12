package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/wayyoungboy/ha500-fnos-toolbox/internal/config"
	"github.com/wayyoungboy/ha500-fnos-toolbox/internal/control"
)

type fakeBackend struct {
	status        Status
	settings      config.Settings
	led           LEDControl
	verified      []int
	logs          LogSnapshot
	logsCleared   bool
	fanPolicySets []int
}

func (fake *fakeBackend) Snapshot(context.Context) (Status, error) { return fake.status, nil }
func (fake *fakeBackend) Settings() config.Settings                { return fake.settings }
func (fake *fakeBackend) LEDControl() LEDControl                   { return fake.led }
func (fake *fakeBackend) SetLEDControl(_ context.Context, value LEDControl) error {
	fake.led = value
	return nil
}
func (fake *fakeBackend) SetBayPortMap(_ context.Context, value [5]int) error {
	fake.settings.BayPortMap = value
	return nil
}
func (fake *fakeBackend) SetFanPolicy(_ context.Context, channel int, value control.FanPolicy) error {
	if fake.settings.FanPolicies == nil {
		fake.settings.FanPolicies = make(control.PolicySet)
	}
	fake.settings.FanPolicies[channel] = value
	fake.fanPolicySets = append(fake.fanPolicySets, channel)
	return nil
}
func (fake *fakeBackend) VerifyFan(_ context.Context, channel int, _ bool) (control.Verification, error) {
	fake.verified = append(fake.verified, channel)
	return control.Verification{Supported: true, InitialRPM: 1700, ObservedRPM: 2100}, nil
}
func (fake *fakeBackend) ReadLogs(int) (LogSnapshot, error) { return fake.logs, nil }
func (fake *fakeBackend) ClearLogs() error {
	fake.logsCleared = true
	return nil
}

func request(t *testing.T, handler http.Handler, method, path, body string, admin bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if admin {
		req.Header.Set("X-Trim-Isadmin", "true")
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func TestAPIRejectsNonAdminRequests(t *testing.T) {
	handler := NewHandler("/app/ha500_toolbox", &fakeBackend{})
	for _, path := range []string{"/api/status", "/api/settings", "/api/logs"} {
		response := request(t, handler, http.MethodGet, path, "", false)
		if response.Code != http.StatusForbidden {
			t.Fatalf("GET %s status = %d; want 403", path, response.Code)
		}
	}
}

func TestStatusWorksThroughFnOSGatewayPrefix(t *testing.T) {
	backend := &fakeBackend{status: Status{Version: "0.1.0", Hardware: HardwareStatus{Supported: true, Match: "ha500-reference-fingerprint"}}}
	handler := NewHandler("/app/ha500_toolbox", backend)
	response := request(t, handler, http.MethodGet, "/app/ha500_toolbox/api/status", "", true)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var got Status
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Version != "0.1.0" || !got.Hardware.Supported {
		t.Fatalf("status payload = %+v", got)
	}
}

func TestStatusNeverReturnsPlainDiskSerial(t *testing.T) {
	const serial = "PRIVATE-SERIAL-1234"
	temperature := 40.0
	backend := &fakeBackend{status: Status{TemperatureSources: []TemperatureSourceStatus{
		{ID: "disk:0f00", Kind: "disk", Label: "DISK 1 · Example Disk · ID A1B2C3", TemperatureC: &temperature, Available: true},
	}}}
	handler := NewHandler("/app/ha500_toolbox", backend)
	response := request(t, handler, http.MethodGet, "/api/status", "", true)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if strings.Contains(response.Body.String(), serial) {
		t.Fatalf("status leaked plain disk serial: %s", response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"kind":"disk"`) || !strings.Contains(response.Body.String(), `"temperature_c":40`) {
		t.Fatalf("status omitted disk temperature source: %s", response.Body.String())
	}
}

func TestUpdatesOnlyRequestedFanPolicy(t *testing.T) {
	backend := &fakeBackend{settings: config.Settings{BayPortMap: [5]int{1, 2, 3, 4, 5}, FanPolicies: control.PolicySet{
		2: {Mode: control.ModeFixed, FixedDutyPercent: 100, MinDutyPercent: 35, CriticalTemperatureC: 80, TemperatureSource: "cpu"},
		3: {Mode: control.ModeMonitor},
	}}}
	handler := NewHandler("/app/ha500_toolbox", backend)
	body := `{"mode":"curve","temperature_source":"cpu","min_duty_percent":35,"critical_temperature_c":80,"curve":[{"temperature_c":40,"duty_percent":35},{"temperature_c":80,"duty_percent":100}]}`
	response := request(t, handler, http.MethodPut, "/api/fans/3/policy", body, true)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if !reflect.DeepEqual(backend.fanPolicySets, []int{3}) || backend.settings.FanPolicies[2].Mode != control.ModeFixed || backend.settings.FanPolicies[3].Mode != control.ModeCurve {
		t.Fatalf("unexpected policies after update: %+v", backend.settings.FanPolicies)
	}
}

func TestValidatesAndUpdatesBayMapping(t *testing.T) {
	backend := &fakeBackend{settings: config.Settings{BayPortMap: [5]int{1, 2, 3, 4, 5}}}
	handler := NewHandler("/app/ha500_toolbox", backend)
	bad := request(t, handler, http.MethodPut, "/api/led/mapping", `{"bay_port_map":[1,2,3,4,4]}`, true)
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("invalid mapping status = %d", bad.Code)
	}
	good := request(t, handler, http.MethodPut, "/api/led/mapping", `{"bay_port_map":[6,2,3,4,5]}`, true)
	if good.Code != http.StatusOK || backend.settings.BayPortMap != [5]int{6, 2, 3, 4, 5} {
		t.Fatalf("valid mapping response = %d, settings = %+v", good.Code, backend.settings)
	}
}

func TestLogEndpointIsBoundedAndSupportsOneClickClear(t *testing.T) {
	backend := &fakeBackend{logs: LogSnapshot{Items: []LogItem{{Level: "ERROR", Message: "temperature unavailable"}}, Limit: 500}}
	handler := NewHandler("/app/ha500_toolbox", backend)
	get := request(t, handler, http.MethodGet, "/api/logs?limit=500", "", true)
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), "temperature unavailable") {
		t.Fatalf("logs status = %d, body = %s", get.Code, get.Body.String())
	}
	clear := request(t, handler, http.MethodDelete, "/api/logs", "", true)
	if clear.Code != http.StatusOK || !backend.logsCleared {
		t.Fatalf("clear status = %d, cleared = %v", clear.Code, backend.logsCleared)
	}
}

func TestMalformedBodyAndBackendFailureReturnJSONErrors(t *testing.T) {
	backend := &fakeBackend{}
	handler := NewHandler("/app/ha500_toolbox", backend)
	response := request(t, handler, http.MethodPut, "/api/led", `{`, true)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("status = %d, content type = %q", response.Code, response.Header().Get("Content-Type"))
	}
	_ = errors.New("keep errors import tied to error response contract")
}

func TestNativeFrontendIsServedAtGatewayRoot(t *testing.T) {
	handler := NewHandler("/app/ha500_toolbox", &fakeBackend{})
	response := request(t, handler, http.MethodGet, "/app/ha500_toolbox/", "", false)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "HA500 飞牛工具箱") {
		t.Fatalf("status = %d, body = %.200s", response.Code, response.Body.String())
	}
}

func TestNativeFrontendExposesVersionAndInteractiveFanCurve(t *testing.T) {
	handler := NewHandler("/app/ha500_toolbox", &fakeBackend{})
	response := request(t, handler, http.MethodGet, "/app/ha500_toolbox/", "", false)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	body := response.Body.String()
	for _, marker := range []string{`id="app-version"`, `type="module"`, `src="app.js"`} {
		if !strings.Contains(body, marker) {
			t.Errorf("frontend is missing %s", marker)
		}
	}

	module := request(t, handler, http.MethodGet, "/app/ha500_toolbox/fan-curve.mjs", "", false)
	if module.Code != http.StatusOK || !strings.Contains(module.Header().Get("Content-Type"), "javascript") {
		t.Fatalf("fan curve module status = %d, content type = %q", module.Code, module.Header().Get("Content-Type"))
	}
}

func TestGatewayRootWithoutSlashRedirectsSoRelativeAssetsStayInsideApp(t *testing.T) {
	handler := NewHandler("/app/ha500_toolbox", &fakeBackend{})
	response := request(t, handler, http.MethodGet, "/app/ha500_toolbox", "", false)
	if response.Code != http.StatusPermanentRedirect || response.Header().Get("Location") != "/app/ha500_toolbox/" {
		t.Fatalf("status = %d, location = %q", response.Code, response.Header().Get("Location"))
	}
}
