package module

import (
	"context"
	"net/http"
	"sync"

	"github.com/wayyoungboy/ha500-fnos-toolbox/internal/server"
	"github.com/wayyoungboy/ha500-fnos-toolbox/internal/service"
)

type Options struct {
	SysRoot      string
	CPUInfoPath  string
	SettingsPath string
	LogPath      string
	Version      string
	Prefix       string
}

// Module hosts the HA500 API, UI and control loop in the parent process.
type Module struct {
	service *service.Service
	handler http.Handler
	cancel  context.CancelFunc
	done    chan struct{}
	once    sync.Once
}

func New(parent context.Context, options Options) (*Module, error) {
	runtime, err := service.New(service.Options{
		SysRoot: options.SysRoot, CPUInfoPath: options.CPUInfoPath,
		SettingsPath: options.SettingsPath, LogPath: options.LogPath, Version: options.Version,
	})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	module := &Module{
		service: runtime,
		handler: server.NewHandler(options.Prefix, runtime),
		cancel:  cancel,
		done:    make(chan struct{}),
	}
	go func() {
		defer close(module.done)
		runtime.Run(ctx)
	}()
	return module, nil
}

func (module *Module) Handler() http.Handler { return module.handler }

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
		closeErr = module.service.Close()
	})
	return closeErr
}

func ForceConfiguredFansFull(sysRoot, settingsPath string) error {
	return service.ForceConfiguredFansFull(sysRoot, settingsPath)
}
