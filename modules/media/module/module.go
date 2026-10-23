package module

import (
	"context"
	"net/http"
	"time"

	"github.com/wayyoungboy/fnos-media-acquisition-center/internal/service"
)

// Version is the media runtime version shared with embedding applications.
const Version = service.Version

type Options struct {
	DataDir   string
	PublicDir string
	BundleDir string
	Prefix    string
}

// Module runs the media HTTP API and workers inside its host process.
type Module struct {
	service *service.Service
}

func New(parent context.Context, options Options) (*Module, error) {
	runtime, err := service.NewService(options.DataDir, options.PublicDir, options.BundleDir, options.Prefix, false)
	if err != nil {
		return nil, err
	}
	if err := runtime.Start(parent); err != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = runtime.Shutdown(shutdownCtx)
		return nil, err
	}
	return &Module{service: runtime}, nil
}

func (module *Module) Handler() http.Handler { return module.service.Handler() }

func (module *Module) Close(ctx context.Context) error { return module.service.Shutdown(ctx) }
