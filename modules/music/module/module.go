package module

import (
	"context"
	"net/http"

	"github.com/wayyoungboy/fnos-music-converter/internal/service"
)

type Options struct {
	DataDir     string
	PublicDir   string
	Prefix      string
	BrowseRoots []string
}

// Module hosts the music converter inside the parent process.
type Module struct {
	service *service.Service
}

func New(options Options) (*Module, error) {
	runtime, err := service.New(service.Options{
		DataDir: options.DataDir, PublicDir: options.PublicDir,
		Prefix: options.Prefix, BrowseRoots: options.BrowseRoots,
	})
	if err != nil {
		return nil, err
	}
	return &Module{service: runtime}, nil
}

func (module *Module) Handler() http.Handler { return module.service.Handler() }

func (module *Module) Close(ctx context.Context) error { return module.service.Shutdown(ctx) }
