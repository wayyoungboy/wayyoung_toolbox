package service

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"fnos-backup-center/internal/backup"
	"fnos-backup-center/internal/securefs"
	"fnos-backup-center/internal/tasks"
)

const Version = "0.0.1"
const Prefix = "/app/backup_center"

type gatewayKey struct{}

type Options struct {
	DataDir        string
	PublicDir      string
	Prefix         string
	LocalRoots     []string
	Grants         func(context.Context) ([]string, error)
	Mounts         backup.MountProvider
	LocalDev       bool
	TrustedGateway bool
}
type Server struct {
	Manager  *tasks.Manager
	Engine   *backup.Engine
	options  Options
	csrf     string
	handler  http.Handler
	metadata chan struct{}
	prefix   string
}

func New(o Options) (*Server, error) {
	if !filepath.IsAbs(o.PublicDir) {
		return nil, errors.New("absolute UI directory required")
	}
	for _, r := range o.LocalRoots {
		if !filepath.IsAbs(r) || filepath.Clean(r) != r || r == "/" {
			return nil, errors.New("invalid authorized root")
		}
	}
	if o.Mounts == nil {
		o.Mounts = backup.SystemMounts{}
	}
	prefix := strings.TrimSuffix(o.Prefix, "/")
	if prefix == "" {
		prefix = Prefix
	}
	if !strings.HasPrefix(prefix, "/app/") {
		return nil, errors.New("invalid application prefix")
	}
	s := &Server{options: o, Engine: &backup.Engine{Mounts: o.Mounts}, csrf: backup.NewID() + backup.NewID(), metadata: make(chan struct{}, 4), prefix: prefix}
	m, err := tasks.Open(o.DataDir, &authorizedExecutor{server: s})
	if err != nil {
		return nil, err
	}
	s.Manager = m
	s.handler = s.routes()
	return s, nil
}
func (s *Server) Handler() http.Handler { return s.handler }

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/state", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"version": Version, "csrf": s.csrf, "state": s.Manager.State(), "cloud_notice": backup.CloudNotice, "local_roots": s.options.LocalRoots})
	})
	mux.HandleFunc("GET /api/mounts", func(w http.ResponseWriter, r *http.Request) { m, err := s.remoteMounts(); respond(w, 200, m, err) })
	mux.HandleFunc("GET /api/directories", s.expensive(s.directories))
	mux.HandleFunc("POST /api/tasks", s.saveTask)
	mux.HandleFunc("PUT /api/tasks/{id}", s.saveTask)
	mux.HandleFunc("DELETE /api/tasks/{id}", func(w http.ResponseWriter, r *http.Request) {
		respond(w, 200, map[string]bool{"deleted": true}, s.Manager.DeleteTask(r.PathValue("id")))
	})
	mux.HandleFunc("POST /api/tasks/{id}/run", s.run)
	mux.HandleFunc("GET /api/tasks/{id}/snapshots", s.expensive(s.snapshots))
	mux.HandleFunc("POST /api/import", s.expensive(s.importRepository))
	mux.HandleFunc("POST /api/tasks/{id}/rebind", s.expensive(s.rebind))
	mux.HandleFunc("POST /api/runs/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		respond(w, 200, map[string]bool{"cancel_requested": true}, s.Manager.Cancel(r.PathValue("id")))
	})
	mux.HandleFunc("PUT /api/settings", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Retain int `json:"retain"`
		}
		if err := decode(w, r, &in); err != nil {
			respond(w, 400, nil, err)
			return
		}
		respond(w, 200, map[string]int{"retain": in.Retain}, s.Manager.SetRetention(in.Retain))
	})
	mux.Handle("GET /", http.FileServer(http.Dir(s.options.PublicDir)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'self'")
		w.Header().Set("Cache-Control", "no-store")
		trusted, _ := r.Context().Value(gatewayKey{}).(bool)
		trusted = trusted || s.options.TrustedGateway
		uid, uidErr := strconv.ParseUint(r.Header.Get("X-Trim-Userid"), 10, 32)
		if !s.options.LocalDev && (!trusted || r.Header.Get("X-Trim-Isadmin") != "true" || uidErr != nil || uid == 0) {
			respond(w, 403, nil, errors.New("需要通过飞牛网关以管理员身份访问"))
			return
		}
		if r.URL.Path == s.prefix {
			http.Redirect(w, r, s.prefix+"/", http.StatusPermanentRedirect)
			return
		}
		if !strings.HasPrefix(r.URL.Path, s.prefix+"/") {
			http.NotFound(w, r)
			return
		}
		r = r.Clone(r.Context())
		r.URL.Path = strings.TrimPrefix(r.URL.Path, s.prefix)
		if r.Method != "GET" && r.Method != "HEAD" {
			media, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if media != "application/json" {
				respond(w, 415, nil, errors.New("JSON body required"))
				return
			}
			if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Backup-CSRF")), []byte(s.csrf)) != 1 {
				respond(w, 403, nil, errors.New("请求校验失效，请刷新页面"))
				return
			}
			if !validRequestOrigin(r, trusted, s.prefix) {
				respond(w, 403, nil, errors.New("请求来源校验失败，请从飞牛桌面重新打开应用"))
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) expensive(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		select {
		case s.metadata <- struct{}{}:
			defer func() { <-s.metadata }()
		default:
			respond(w, 429, nil, errors.New("目录操作繁忙，请稍后重试"))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		next(w, r.WithContext(ctx))
	}
}

func (s *Server) task(id string) (tasks.Task, error) {
	for _, t := range s.Manager.State().Tasks {
		if t.ID == id {
			return t, nil
		}
	}
	return tasks.Task{}, errors.New("task not found")
}

func (s *Server) saveTask(w http.ResponseWriter, r *http.Request) {
	var in tasks.Task
	if err := decode(w, r, &in); err != nil {
		respond(w, 400, nil, err)
		return
	}
	code := 200
	if r.Method == "POST" {
		in.ID = ""
		in.ReadOnly = false
		in.Selection.RepoID = backup.NewID()
		code = 201
		mount, err := s.targetMount(in.Selection.Target)
		if err != nil {
			respond(w, 400, nil, err)
			return
		}
		in.Selection.Mount = mount
	} else {
		in.ID = r.PathValue("id")
	}
	if !in.ReadOnly {
		if err := s.local(r.Context(), in.Selection.Source, false); err != nil {
			respond(w, 400, nil, err)
			return
		}
	}
	out, err := s.Manager.SaveTask(in)
	respond(w, code, out, err)
}
func (s *Server) run(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kind        string `json:"kind"`
		SnapshotID  string `json:"snapshot_id"`
		Destination string `json:"destination"`
	}
	if err := decode(w, r, &in); err != nil {
		respond(w, 400, nil, err)
		return
	}
	t, err := s.task(r.PathValue("id"))
	if err != nil {
		respond(w, 404, nil, err)
		return
	}
	if in.Kind == "restore" {
		err = s.local(r.Context(), in.Destination, true)
	} else {
		err = s.local(r.Context(), t.Selection.Source, false)
	}
	if err != nil {
		respond(w, 400, nil, err)
		return
	}
	out, err := s.Manager.Start(t.ID, in.Kind, "manual", in.SnapshotID, in.Destination)
	respond(w, 202, out, err)
}
func (s *Server) snapshots(w http.ResponseWriter, r *http.Request) {
	t, err := s.task(r.PathValue("id"))
	if err != nil {
		respond(w, 404, nil, err)
		return
	}
	out, err := s.Engine.Snapshots(r.Context(), t.Selection)
	respond(w, 200, out, err)
}
func (s *Server) importRepository(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name   string `json:"name"`
		Target string `json:"target"`
		RepoID string `json:"repo_id"`
	}
	if err := decode(w, r, &in); err != nil {
		respond(w, 400, nil, err)
		return
	}
	mount, err := s.targetMount(in.Target)
	if err != nil {
		respond(w, 400, nil, err)
		return
	}
	sel := backup.Selection{Target: in.Target, RepoID: in.RepoID, Mount: mount}
	snapshots, err := s.Engine.Snapshots(r.Context(), sel)
	if err == nil && len(snapshots) == 0 {
		err = errors.New("未找到可恢复的有效备份快照")
	}
	if err != nil {
		respond(w, 400, nil, err)
		return
	}
	out, err := s.Manager.SaveTask(tasks.Task{Name: in.Name, Selection: sel, ReadOnly: true})
	respond(w, 201, out, err)
}

func (s *Server) rebind(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Target string `json:"target"`
	}
	if err := decode(w, r, &in); err != nil {
		respond(w, 400, nil, err)
		return
	}
	t, err := s.task(r.PathValue("id"))
	if err != nil {
		respond(w, 404, nil, err)
		return
	}
	m, err := s.targetMount(in.Target)
	if err != nil {
		respond(w, 400, nil, err)
		return
	}
	sel := t.Selection
	sel.Target = in.Target
	sel.Mount = m
	all, err := s.Engine.Snapshots(r.Context(), sel)
	if err == nil && len(all) == 0 {
		err = errors.New("新挂载中未找到原仓库的有效快照，请确认选择的是同一份备份")
	}
	if err != nil {
		respond(w, 400, nil, err)
		return
	}
	out, err := s.Manager.Rebind(t.ID, sel)
	respond(w, 200, out, err)
}

func decode(w http.ResponseWriter, r *http.Request, out any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return errors.New("请求内容无效或过大")
	}
	if err := d.Decode(&struct{}{}); err != io.EOF {
		return errors.New("请求包含多余内容")
	}
	return nil
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func respond(w http.ResponseWriter, status int, v any, err error) {
	if err != nil {
		if status < 400 {
			status = 400
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, status, v)
}

type authorizedExecutor struct{ server *Server }

func (e *authorizedExecutor) Backup(ctx context.Context, s backup.Selection, p func(backup.Progress)) (backup.Result, error) {
	if err := e.server.local(ctx, s.Source, false); err != nil {
		return backup.Result{State: "failed"}, err
	}
	return e.server.Engine.Backup(ctx, s, p)
}
func (e *authorizedExecutor) Preview(ctx context.Context, s backup.Selection) (backup.Preview, error) {
	if err := e.server.local(ctx, s.Source, false); err != nil {
		return backup.Preview{}, err
	}
	return e.server.Engine.Preview(ctx, s)
}
func (e *authorizedExecutor) Restore(ctx context.Context, s backup.Selection, id, dest string, p func(backup.Progress)) (backup.Result, error) {
	if err := e.server.local(ctx, dest, true); err != nil {
		return backup.Result{State: "failed"}, err
	}
	return e.server.Engine.Restore(ctx, s, id, dest, p)
}

func (s *Server) roots(ctx context.Context) ([]string, error) {
	paths := append([]string{}, s.options.LocalRoots...)
	if s.options.Grants != nil {
		granted, err := s.options.Grants(ctx)
		if err != nil {
			return nil, err
		}
		paths = append(paths, granted...)
	}
	return paths, nil
}

func (s *Server) local(ctx context.Context, name string, newDirectory bool) error {
	if !filepath.IsAbs(name) || filepath.Clean(name) != name || name == "/" {
		return errors.New("请选择有效的绝对目录")
	}
	allowed := false
	roots, err := s.roots(ctx)
	if err != nil {
		return err
	}
	for _, root := range roots {
		if backup.Within(root, name) {
			allowed = true
			break
		}
	}
	if !allowed {
		return errors.New("目录未获应用授权，请在应用中心授予目录访问权限后重启应用")
	}
	check := name
	if newDirectory {
		check = filepath.Dir(name)
	}
	all, err := s.options.Mounts.List()
	if err != nil {
		return err
	}
	if m, err := backup.Covering(all, check); err == nil && strings.HasPrefix(m.Type, "fuse.") {
		return errors.New("源目录和恢复目录必须位于本地存储")
	}
	root, err := securefs.OpenRoot(check)
	if err != nil {
		return errors.New("目录不存在、含符号链接或应用没有访问权限")
	}
	return root.Close()
}
