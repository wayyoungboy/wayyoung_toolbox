package service

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"

	"fnos-backup-center/internal/backup"
	"fnos-backup-center/internal/securefs"
)

type Directory struct {
	Name string `json:"name"`
	Path string `json:"path"`
}
type DirectoryList struct {
	Path      string      `json:"path"`
	Parent    string      `json:"parent"`
	Roots     []Directory `json:"roots"`
	Entries   []Directory `json:"entries"`
	Truncated bool        `json:"truncated"`
}

func (s *Server) remoteMounts() ([]backup.Mount, error) {
	all, err := s.options.Mounts.List()
	if err != nil {
		return nil, err
	}
	out := []backup.Mount{}
	for _, m := range all {
		if m.Type == "fuse.rclone" {
			out = append(out, m)
		}
	}
	return out, nil
}
func (s *Server) targetMount(name string) (backup.Mount, error) {
	all, err := s.options.Mounts.List()
	if err != nil {
		return backup.Mount{}, err
	}
	m, err := backup.Covering(all, name)
	if err != nil || m.Type != "fuse.rclone" {
		return m, errors.New("请选择当前已挂载的网盘目录")
	}
	v, err := backup.Pin(s.options.Mounts, m, name)
	if err != nil {
		return m, err
	}
	v.Close()
	return m, nil
}

func (s *Server) directories(w http.ResponseWriter, r *http.Request) {
	kind, name := r.URL.Query().Get("kind"), r.URL.Query().Get("path")
	out := DirectoryList{Roots: []Directory{}, Entries: []Directory{}}
	if kind != "source" && kind != "target" && kind != "restore" {
		respond(w, 400, nil, errors.New("invalid directory purpose"))
		return
	}
	if kind == "target" {
		mounts, err := s.remoteMounts()
		if err != nil {
			respond(w, 400, nil, err)
			return
		}
		for _, m := range mounts {
			out.Roots = append(out.Roots, Directory{Name: "远程挂载 · " + filepath.Base(m.Point), Path: m.Point})
		}
	} else {
		roots, err := s.roots(r.Context())
		if err != nil {
			respond(w, 400, nil, err)
			return
		}
		for _, p := range roots {
			out.Roots = append(out.Roots, Directory{Name: filepath.Base(p), Path: p})
		}
	}
	if name == "" {
		respond(w, 200, out, nil)
		return
	}
	var root *securefs.Root
	var volume *backup.Volume
	var err error
	var boundary string
	if kind == "target" {
		var m backup.Mount
		m, err = s.targetMount(name)
		if err == nil {
			volume, err = backup.Pin(s.options.Mounts, m, name)
		}
		if err == nil {
			root = volume.Root
			boundary = m.Point
			defer volume.Close()
		}
	} else {
		err = s.local(r.Context(), name, false)
		if err == nil {
			root, err = securefs.OpenRoot(name)
		}
		if err == nil {
			defer root.Close()
			for _, p := range out.Roots {
				if backup.Within(p.Path, name) && len(p.Path) > len(boundary) {
					boundary = p.Path
				}
			}
		}
	}
	if err != nil {
		respond(w, 400, nil, err)
		return
	}
	fd, err := root.OpenDir(".")
	if err != nil {
		respond(w, 400, nil, err)
		return
	}
	defer fd.Close()
	entries, err := fd.ReadDir(2001)
	if err != nil && err != io.EOF {
		respond(w, 400, nil, err)
		return
	}
	out.Truncated = len(entries) > 2000
	if out.Truncated {
		entries = entries[:2000]
	}
	for _, entry := range entries {
		if entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
			out.Entries = append(out.Entries, Directory{Name: entry.Name(), Path: filepath.Join(name, entry.Name())})
		}
	}
	sort.Slice(out.Entries, func(i, j int) bool { return out.Entries[i].Name < out.Entries[j].Name })
	out.Path = name
	if name != boundary {
		out.Parent = filepath.Dir(name)
	}
	if volume != nil {
		if err := volume.Check(); err != nil {
			respond(w, 400, nil, err)
			return
		}
	}
	respond(w, 200, out, nil)
}
