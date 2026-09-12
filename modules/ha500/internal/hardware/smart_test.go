package hardware

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSmartctlReaderUsesStandbySafeJSONAndAcceptsHealthExitBits(t *testing.T) {
	script := filepath.Join(t.TempDir(), "smartctl")
	body := `#!/bin/sh
if [ "$*" != "-d sat -n standby,0 -j -a /dev/sda" ]; then
  exit 98
fi
printf '%s\n' '{"serial_number":"PRIVATE-SERIAL-1234","model_name":"Example Disk","temperature":{"current":41}}'
exit 64
`
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}

	identity, err := NewSmartctlReader(script).Read(context.Background(), "sda")
	if err != nil {
		t.Fatalf("Read returned error for valid SMART JSON: %v", err)
	}
	if identity.Serial != "PRIVATE-SERIAL-1234" || identity.Model != "Example Disk" || identity.TemperatureC == nil || *identity.TemperatureC != 41 {
		t.Fatalf("identity = %+v", identity)
	}
	if _, err := NewSmartctlReader(script).Read(context.Background(), "../../etc/passwd"); err == nil {
		t.Fatal("unsafe block device name was accepted")
	}
}

func TestSmartctlReaderRejectsOperationalExitBitsEvenWithValidJSON(t *testing.T) {
	script := filepath.Join(t.TempDir(), "smartctl")
	body := `#!/bin/sh
printf '%s\n' '{"serial_number":"PRIVATE-SERIAL-1234","model_name":"Example Disk","temperature":{"current":41}}'
exit 4
`
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}

	if _, err := NewSmartctlReader(script).Read(context.Background(), "sda"); err == nil {
		t.Fatal("SMART command failure bit was accepted")
	}
}

func TestSmartctlReaderEnforcesTimeoutAndBoundedOutput(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		script := filepath.Join(t.TempDir(), "smartctl")
		if err := os.WriteFile(script, []byte("#!/bin/sh\ntrap '' TERM\nsleep 30\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		reader := NewSmartctlReaderWithOptions(script, SmartctlReaderOptions{Timeout: 100 * time.Millisecond, OutputLimit: 128, ErrorLimit: 64})
		started := time.Now()
		_, err := reader.Read(context.Background(), "sda")
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Read error = %v; want context deadline exceeded", err)
		}
		if elapsed := time.Since(started); elapsed > 2*time.Second {
			t.Fatalf("timed out process was not reclaimed promptly: %v", elapsed)
		}
	})

	t.Run("stdout limit", func(t *testing.T) {
		script := filepath.Join(t.TempDir(), "smartctl")
		body := "#!/bin/sh\ni=0\nwhile [ $i -lt 256 ]; do printf x; i=$((i+1)); done\n"
		if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
		reader := NewSmartctlReaderWithOptions(script, SmartctlReaderOptions{Timeout: time.Second, OutputLimit: 128, ErrorLimit: 64})
		_, err := reader.Read(context.Background(), "sda")
		if err == nil || !strings.Contains(err.Error(), "exceeds 128 bytes") {
			t.Fatalf("Read error = %v; want bounded-output error", err)
		}
	})

	t.Run("capped writer", func(t *testing.T) {
		buffer := &cappedBuffer{limit: 8}
		first, firstErr := buffer.Write([]byte("123456"))
		second, secondErr := buffer.Write([]byte("789012"))
		if firstErr != nil || secondErr != nil || first != 6 || second != 6 || buffer.buffer.String() != "12345678" || !buffer.truncated {
			t.Fatalf("buffer=%q truncated=%v writes=(%d,%d) errors=(%v,%v)", buffer.buffer.String(), buffer.truncated, first, second, firstErr, secondErr)
		}
	})
}

type fakeSMARTReader struct {
	mu      sync.Mutex
	results map[string]SMARTIdentity
	calls   []string
}

func (reader *fakeSMARTReader) Read(_ context.Context, device string) (SMARTIdentity, error) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	reader.calls = append(reader.calls, device)
	return reader.results[device], nil
}

func TestSMARTMonitorKeysSourcesBySerialFingerprintAndCachesReads(t *testing.T) {
	temperature := 39.5
	reader := &fakeSMARTReader{results: map[string]SMARTIdentity{
		"sda": {Serial: "PRIVATE-SERIAL-1234", Model: "Example Disk", TemperatureC: &temperature},
	}}
	monitor := NewSMARTMonitor(reader, time.Hour)
	disks := map[int]Disk{2: {Device: "sda", Model: "Example Disk", ControllerPort: 2}}

	first, err := monitor.Snapshot(context.Background(), disks)
	if err != nil {
		t.Fatal(err)
	}
	second, err := monitor.Snapshot(context.Background(), disks)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || len(second) != 1 || len(reader.calls) != 1 {
		t.Fatalf("first=%+v second=%+v calls=%v", first, second, reader.calls)
	}
	wantSource := DiskTemperatureSourceID("PRIVATE-SERIAL-1234")
	if first[0].SourceID != wantSource || first[0].DisplayID == "" || first[0].TemperatureC != 39.5 || !first[0].Available {
		t.Fatalf("disk temperature = %+v", first[0])
	}
	if strings.Contains(first[0].DisplayID, "1234") {
		t.Fatalf("display ID leaked serial material: %q", first[0].DisplayID)
	}
}

func TestSMARTMonitorKeepsIdentityWhenDeviceNameChanges(t *testing.T) {
	temperature := 37.0
	reader := &fakeSMARTReader{results: map[string]SMARTIdentity{
		"sda": {Serial: "STABLE-SERIAL", Model: "Disk", TemperatureC: &temperature},
		"sdc": {Serial: "STABLE-SERIAL", Model: "Disk", TemperatureC: &temperature},
	}}
	monitor := NewSMARTMonitor(reader, 0)

	before, err := monitor.Snapshot(context.Background(), map[int]Disk{1: {Device: "sda", ControllerPort: 1}})
	if err != nil {
		t.Fatal(err)
	}
	after, err := monitor.Snapshot(context.Background(), map[int]Disk{4: {Device: "sdc", ControllerPort: 4}})
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 1 || len(after) != 1 || before[0].SourceID != after[0].SourceID {
		t.Fatalf("source changed with device name: before=%+v after=%+v", before, after)
	}
	if after[0].Device != "sdc" || after[0].ControllerPort != 4 {
		t.Fatalf("current topology was not refreshed: %+v", after[0])
	}
}

type blockingSMARTReader struct {
	mu      sync.Mutex
	calls   int
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (reader *blockingSMARTReader) Read(context.Context, string) (SMARTIdentity, error) {
	reader.mu.Lock()
	reader.calls++
	reader.mu.Unlock()
	reader.once.Do(func() { close(reader.started) })
	<-reader.release
	temperature := 40.0
	return SMARTIdentity{Serial: "ONE-SERIAL", Model: "Disk", TemperatureC: &temperature}, nil
}

func TestConcurrentSMARTSnapshotsShareOneRefresh(t *testing.T) {
	reader := &blockingSMARTReader{started: make(chan struct{}), release: make(chan struct{})}
	monitor := NewSMARTMonitor(reader, time.Hour)
	disks := map[int]Disk{1: {Device: "sda", ControllerPort: 1}}
	done := make(chan error, 2)
	go func() {
		_, err := monitor.Snapshot(context.Background(), disks)
		done <- err
	}()
	<-reader.started
	go func() {
		_, err := monitor.Snapshot(context.Background(), disks)
		done <- err
	}()
	close(reader.release)
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if reader.calls != 1 {
		t.Fatalf("SMART reader calls = %d; want one shared refresh", reader.calls)
	}
}
