package backup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"fnos-backup-center/internal/securefs"
)

var ErrMountLost = errors.New("目标挂载丢失或身份发生变化，已停止操作")

type Mount struct {
	ID     int    `json:"id"`
	Point  string `json:"point"`
	Source string `json:"source"`
	Type   string `json:"type"`
	Root   string `json:"root"`
}

func (m Mount) SameIdentity(other Mount) bool {
	return m.Point == other.Point && m.Source == other.Source && m.Type == other.Type && m.Root == other.Root
}

type MountProvider interface {
	List() ([]Mount, error)
	FileID(*os.File) (int, error)
}
type SystemMounts struct{}

func (SystemMounts) List() ([]Mount, error) {
	b, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}
	return ParseMountInfo(string(b))
}
func (SystemMounts) FileID(f *os.File) (int, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/self/fdinfo/%d", f.Fd()))
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "mnt_id:" {
			return strconv.Atoi(fields[1])
		}
	}
	return 0, errors.New("kernel mount ID unavailable")
}

func ParseMountInfo(data string) ([]Mount, error) {
	var result []Mount
	unescape := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	for _, line := range strings.Split(strings.TrimSpace(data), "\n") {
		if line == "" {
			continue
		}
		left, right, ok := strings.Cut(line, " - ")
		l, r := strings.Fields(left), strings.Fields(right)
		if !ok || len(l) < 6 || len(r) < 3 {
			return nil, errors.New("invalid mountinfo record")
		}
		id, err := strconv.Atoi(l[0])
		if err != nil || id <= 0 {
			return nil, errors.New("invalid mount ID")
		}
		point := unescape.Replace(l[4])
		if !filepath.IsAbs(point) {
			return nil, errors.New("invalid mount point")
		}
		result = append(result, Mount{ID: id, Point: point, Root: unescape.Replace(l[3]), Type: r[0], Source: unescape.Replace(r[1])})
	}
	return result, nil
}

func Within(root, name string) bool {
	rel, err := filepath.Rel(root, name)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func Covering(mounts []Mount, name string) (Mount, error) {
	var best Mount
	for _, m := range mounts {
		if Within(m.Point, name) && len(m.Point) > len(best.Point) {
			best = m
		}
	}
	if best.Point == "" {
		return Mount{}, ErrMountLost
	}
	return best, nil
}

type Volume struct {
	Root   *securefs.Root
	table  MountProvider
	pinned Mount
	target string
}

func Pin(table MountProvider, want Mount, target string) (*Volume, error) {
	if table == nil || want.Type != "fuse.rclone" || !filepath.IsAbs(target) || filepath.Clean(target) != target {
		return nil, ErrMountLost
	}
	all, err := table.List()
	if err != nil {
		return nil, fmt.Errorf("read mounts: %w", err)
	}
	current, err := Covering(all, target)
	if err != nil || !want.SameIdentity(current) {
		return nil, ErrMountLost
	}
	root, err := securefs.OpenRoot(target)
	if err != nil {
		return nil, err
	}
	v := &Volume{Root: root, table: table, pinned: current, target: target}
	id, err := table.FileID(root.File())
	if err != nil || id != current.ID {
		root.Close()
		return nil, ErrMountLost
	}
	if err = v.Check(); err != nil {
		root.Close()
		return nil, err
	}
	return v, nil
}

func (v *Volume) Close() error { return v.Root.Close() }
func (v *Volume) Check() error {
	all, err := v.table.List()
	if err != nil {
		return fmt.Errorf("mount check: %w", err)
	}
	now, err := Covering(all, v.target)
	if err != nil || now.ID != v.pinned.ID || !now.SameIdentity(v.pinned) {
		return ErrMountLost
	}
	return nil
}
