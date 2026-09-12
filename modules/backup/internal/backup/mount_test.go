package backup

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

type fakeMounts struct {
	mu     sync.Mutex
	items  []Mount
	fileID int
	err    error
}

func (m *fakeMounts) List() ([]Mount, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Mount(nil), m.items...), m.err
}
func (m *fakeMounts) FileID(*os.File) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.fileID, m.err
}
func (m *fakeMounts) lose() { m.mu.Lock(); m.items = nil; m.mu.Unlock() }

func TestParseMountInfoAndEscapes(t *testing.T) {
	data := "41 1 0:50 / /mnt/cloud\\040drive rw,nosuid - fuse.rclone remote:path rw\n42 1 8:1 / / rw - ext4 /dev/sda rw\n"
	m, err := ParseMountInfo(data)
	if err != nil || len(m) != 2 {
		t.Fatalf("%v %v", m, err)
	}
	if m[0].Point != "/mnt/cloud drive" || m[0].Source != "remote:path" || m[0].ID != 41 {
		t.Fatal(m[0])
	}
	for _, s := range []string{"broken", "x 1 0:50 / /mnt rw - fuse.rclone x rw", "41 1 0:50 / /mnt rw"} {
		if _, err := ParseMountInfo(s); err == nil {
			t.Fatalf("accepted %q", s)
		}
	}
}

func TestPinRejectsMissingWrongOrRacedMount(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	want := Mount{ID: 50, Point: dir, Source: "test:remote", Type: "fuse.rclone", Root: "/"}
	for _, tc := range []struct {
		name string
		list []Mount
		id   int
		err  error
	}{
		{"missing", nil, 50, nil},
		{"wrong remote", []Mount{{ID: 50, Point: dir, Source: "other", Type: "fuse.rclone", Root: "/"}}, 50, nil},
		{"wrong descriptor", []Mount{want}, 51, nil},
		{"provider error", []Mount{want}, 50, errors.New("mountinfo unavailable")},
		{"nested", []Mount{want, {ID: 51, Point: filepath.Join(dir, "child"), Source: "other", Type: "tmpfs", Root: "/"}}, 50, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := dir
			if tc.name == "nested" {
				p = filepath.Join(dir, "child")
				os.Mkdir(p, 0700)
			}
			v, err := Pin(&fakeMounts{items: tc.list, fileID: tc.id, err: tc.err}, want, p)
			if err == nil {
				v.Close()
				t.Fatal("unsafe mount accepted")
			}
		})
	}
	provider := &fakeMounts{items: []Mount{want}, fileID: 50}
	v, err := Pin(provider, want, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	provider.lose()
	if err := v.Check(); err == nil {
		t.Fatal("lost mount remained usable")
	}
}
