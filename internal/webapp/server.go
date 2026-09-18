package webapp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/wayyoungboy/wayyoung_toolbox/internal/supervisor"
)

type ModuleManager interface {
	List() []supervisor.Info
	SetEnabled(id string, enabled bool, confirmation string) error
	Route(id string) (supervisor.Definition, http.Handler, error)
}

type Server struct {
	version string
	prefix  string
	public  string
	manager ModuleManager
	handler http.Handler
}

func New(prefix, public string, manager ModuleManager, version string) (*Server, error) {
	prefix = strings.TrimSuffix(prefix, "/")
	if prefix == "" || !strings.HasPrefix(prefix, "/app/") {
		return nil, errors.New("invalid application prefix")
	}
	if !filepath.IsAbs(public) {
		return nil, errors.New("public directory must be absolute")
	}
	if info, err := os.Stat(filepath.Join(public, "index.html")); err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("dashboard assets are unavailable")
	}
	server := &Server{prefix: prefix, public: public, manager: manager, version: version}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/modules", server.listModules)
	mux.HandleFunc("PUT /api/modules/{id}", server.setModule)
	mux.HandleFunc("/modules/", server.serveModule)
	mux.Handle("/", http.FileServer(http.Dir(public)))
	server.handler = securityHeaders(mux)
	return server, nil
}

func (server *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path == server.prefix {
		http.Redirect(writer, request, server.prefix+"/", http.StatusPermanentRedirect)
		return
	}
	if !strings.HasPrefix(request.URL.Path, server.prefix+"/") {
		http.NotFound(writer, request)
		return
	}
	copy := request.Clone(request.Context())
	copy.URL.Path = strings.TrimPrefix(request.URL.Path, server.prefix)
	server.handler.ServeHTTP(writer, copy)
}

func (server *Server) listModules(writer http.ResponseWriter, request *http.Request) {
	if !isAdmin(request) {
		writeError(writer, http.StatusForbidden, "需要飞牛管理员权限，请使用管理员账户打开工具箱")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"modules": server.manager.List(), "process_model": "single", "version": server.version, "is_admin": true})
}

func (server *Server) setModule(writer http.ResponseWriter, request *http.Request) {
	if !isAdmin(request) {
		writeError(writer, http.StatusForbidden, "需要飞牛管理员权限")
		return
	}
	var payload struct {
		Enabled      bool   `json:"enabled"`
		Confirmation string `json:"confirmation"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	if err := server.manager.SetEnabled(request.PathValue("id"), payload.Enabled, payload.Confirmation); err != nil {
		status := http.StatusConflict
		if errors.Is(err, os.ErrNotExist) {
			status = http.StatusNotFound
		}
		writeError(writer, status, err.Error())
		return
	}
	server.listModules(writer, request)
}

func (server *Server) serveModule(writer http.ResponseWriter, request *http.Request) {
	trimmed := strings.TrimPrefix(request.URL.Path, "/modules/")
	parts := strings.SplitN(trimmed, "/", 2)
	id := parts[0]
	if id == "" {
		http.NotFound(writer, request)
		return
	}
	modulePrefix := server.prefix + "/modules/" + id
	if len(parts) == 1 {
		http.Redirect(writer, request, modulePrefix+"/", http.StatusPermanentRedirect)
		return
	}
	_, handler, err := server.manager.Route(id)
	if err != nil {
		status := http.StatusServiceUnavailable
		if errors.Is(err, os.ErrNotExist) {
			status = http.StatusNotFound
		}
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		writer.WriteHeader(status)
		fmt.Fprintf(writer, "<!doctype html><meta charset=utf-8><title>模块未运行</title><style>body{font-family:system-ui;padding:40px;background:#f5f7fb;color:#142033}a{color:#2563eb}</style><h1>模块未运行</h1><p>%s</p><p><a href=%q>返回工具箱</a></p>", htmlEscape(err.Error()), server.prefix+"/")
		return
	}
	forwarded := request.Clone(request.Context())
	forwarded.URL.Path = server.prefix + request.URL.Path
	forwarded.URL.RawPath = ""
	handler.ServeHTTP(writer, forwarded)
}

func isAdmin(request *http.Request) bool {
	return strings.EqualFold(request.Header.Get("X-Trim-Isadmin"), "true")
}

func decodeJSON(request *http.Request, target any) error {
	defer request.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(request.Body, 4097))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("请求内容无效")
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("请求内容无效")
	}
	return nil
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeError(writer http.ResponseWriter, status int, message string) {
	writeJSON(writer, status, map[string]string{"error": message})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Referrer-Policy", "same-origin")
		writer.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'self'")
		writer.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(writer, request)
	})
}

func htmlEscape(value string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;")
	return replacer.Replace(value)
}
