package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"mock-integration-server/internal/admin"
	"mock-integration-server/internal/config"
	"mock-integration-server/internal/engine"
	"mock-integration-server/internal/journal"
	"mock-integration-server/internal/transport"
)

type App struct {
	cfg          *config.Config
	logger       *slog.Logger
	version      string
	engine       *engine.Engine
	journal      *journal.Journal
	stopOnce     sync.Once
	runtimeMu    sync.RWMutex
	reloadMu     sync.Mutex
	stop         context.CancelFunc
	reloadConfig func() (*config.Config, error)
	httpHandler  *swappableHandler
}

func New(c *config.Config, l *slog.Logger, v string) (*App, error) {
	if l == nil {
		return nil, errors.New("logger is required")
	}
	return &App{cfg: c, logger: l, version: v, engine: engine.New(c), journal: journal.New(5000)}, nil
}
func (a *App) Run(ctx context.Context, shutdown time.Duration, shutdownAfter time.Duration) error {
	runCtx, cancel := context.WithCancel(ctx)
	a.stop = cancel
	defer cancel()
	a.httpHandler = newSwappableHandler(transport.NewHTTPHandler(a.cfg, a.engine, a.journal, a.logger))
	mainSrv := &http.Server{Addr: a.cfg.Server.HTTPListen, Handler: a.httpHandler, ReadTimeout: time.Duration(a.cfg.Server.ReadTimeout), WriteTimeout: time.Duration(a.cfg.Server.WriteTimeout), IdleTimeout: time.Duration(a.cfg.Server.IdleTimeout)}
	servers := []*http.Server{mainSrv}
	errs := make(chan error, len(servers)+1)
	go func() {
		a.logger.Info("HTTP listener started", "address", mainSrv.Addr, "version", a.version)
		if err := mainSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- err
		}
	}()
	if a.cfg.Admin.Enabled && a.cfg.Server.AdminListen != "" {
		adminSrv := &http.Server{Addr: a.cfg.Server.AdminListen, Handler: admin.New(a.journal, a.engine, a.RequestShutdown, a.reload), ReadHeaderTimeout: 5 * time.Second}
		servers = append(servers, adminSrv)
		go func() {
			a.logger.Info("admin listener started", "address", adminSrv.Addr)
			if err := adminSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errs <- err
			}
		}()
	}
	var timer *time.Timer
	if shutdownAfter > 0 {
		timer = time.AfterFunc(shutdownAfter, func() {
			a.logger.Warn("automatic shutdown timer elapsed", "after", shutdownAfter.String())
			a.RequestShutdown()
		})
		defer timer.Stop()
		a.logger.Info("automatic shutdown scheduled", "after", shutdownAfter.String())
	}
	select {
	case <-runCtx.Done():
	case err := <-errs:
		for _, s := range servers {
			c, cancel := context.WithTimeout(context.Background(), shutdown)
			_ = s.Shutdown(c)
			cancel()
		}
		return fmt.Errorf("listener failed: %w", err)
	}
	c, cancel := context.WithTimeout(context.Background(), shutdown)
	defer cancel()
	var first error
	for _, s := range servers {
		if err := s.Shutdown(c); err != nil && first == nil {
			first = err
		}
	}
	if first != nil {
		return first
	}
	a.logger.Info("server stopped")
	return nil
}

// RequestShutdown requests graceful shutdown. It is safe to call concurrently.
func (a *App) RequestShutdown() {
	a.stopOnce.Do(func() {
		if a.stop != nil {
			a.logger.Warn("shutdown requested")
			a.stop()
		}
	})
}

// SetConfigReloader configures how the next YAML snapshot is loaded and validated.
func (a *App) SetConfigReloader(fn func() (*config.Config, error)) { a.reloadConfig = fn }

func (a *App) reload() (*engine.Engine, error) {
	a.reloadMu.Lock()
	defer a.reloadMu.Unlock()
	if a.reloadConfig == nil {
		return nil, errors.New("configuration reload is not configured")
	}
	next, err := a.reloadConfig()
	if err != nil {
		return nil, err
	}
	a.runtimeMu.RLock()
	currentCfg := a.cfg
	a.runtimeMu.RUnlock()
	if next.Server.HTTPListen != currentCfg.Server.HTTPListen || next.Server.AdminListen != currentCfg.Server.AdminListen {
		return nil, errors.New("reload cannot change listener addresses; restart server to apply listener changes")
	}
	nextEngine := engine.New(next)
	a.httpHandler.Store(transport.NewHTTPHandler(next, nextEngine, a.journal, a.logger))
	a.runtimeMu.Lock()
	a.cfg = next
	a.engine = nextEngine
	a.runtimeMu.Unlock()
	a.logger.Info("configuration reloaded", "routes", len(next.Routes), "scenarios", len(next.Scenarios))
	return nextEngine, nil
}

type handlerBox struct{ h http.Handler }
type swappableHandler struct{ current atomic.Pointer[handlerBox] }

func newSwappableHandler(h http.Handler) *swappableHandler {
	s := &swappableHandler{}
	s.Store(h)
	return s
}
func (s *swappableHandler) Store(h http.Handler) { s.current.Store(&handlerBox{h: h}) }
func (s *swappableHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	box := s.current.Load()
	if box == nil {
		http.Error(w, "handler not ready", http.StatusServiceUnavailable)
		return
	}
	box.h.ServeHTTP(w, r)
}
