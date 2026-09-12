package module

import (
	"context"
	"net/http"
	"os"
	"sync"
	"time"

	"fnos-backup-center/internal/fnos"
	"fnos-backup-center/internal/service"
)

type Options struct {
	DataDir    string
	PublicDir  string
	Prefix     string
	LocalRoots []string
}

// Module runs the backup HTTP API and scheduler inside its host process.
type Module struct {
	server *service.Server
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

func New(parent context.Context, options Options) (*Module, error) {
	server, err := service.New(service.Options{
		DataDir:        options.DataDir,
		PublicDir:      options.PublicDir,
		Prefix:         options.Prefix,
		LocalRoots:     options.LocalRoots,
		Grants:         fnos.NewClient(func() string { return os.Getenv("TRIM_API_TOKEN") }).SharedFolders,
		TrustedGateway: true,
	})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	module := &Module{server: server, cancel: cancel, done: make(chan struct{})}
	go module.schedule(ctx)
	return module, nil
}

func (module *Module) Handler() http.Handler { return module.server.Handler() }

func (module *Module) schedule(ctx context.Context) {
	defer close(module.done)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			_ = module.server.Manager.Tick(now)
		}
	}
}

func (module *Module) Close(ctx context.Context) error {
	var closeErr error
	module.once.Do(func() {
		module.cancel()
		select {
		case <-module.done:
		case <-ctx.Done():
			closeErr = ctx.Err()
			return
		}
		closeErr = module.server.Manager.Close(ctx)
	})
	return closeErr
}
