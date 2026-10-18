package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"mock-integration-server/internal/app"
	"mock-integration-server/internal/config"
	"mock-integration-server/internal/openapi"
)

var version = "dev"

func main() {
	fs := flag.NewFlagSet("mockserver", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", "config/server.toml", "Path to TOML configuration file (legacy YAML is also supported)")
	listen := fs.String("listen", "", "Override HTTP listen address, e.g. 127.0.0.1:8090")
	adminListen := fs.String("admin-listen", "", "Override admin listen address; use off to disable admin listener")
	noStrict := fs.Bool("no-strict", false, "Disable strict unmatched-request logging, overriding configuration")
	fixturesDir := fs.String("fixtures", "", "Override fixtures root directory")
	logLevel := fs.String("log-level", "", "Log level: debug, info, warn, error")
	logFormat := fs.String("log-format", "", "Log format: text or json")
	logFile := fs.String("log-file", "", "Write logs to this file in addition to stdout; empty disables file logging")
	strict := fs.Bool("strict", false, "Treat unmatched requests as test failures in journal")
	maxBody := fs.Int64("max-body-bytes", 0, "Maximum request body size; 0 uses configuration")
	readTimeout := fs.Duration("read-timeout", 0, "HTTP read timeout, e.g. 30s; 0 uses configuration")
	writeTimeout := fs.Duration("write-timeout", 0, "HTTP write timeout, e.g. 30s; 0 uses configuration")
	idleTimeout := fs.Duration("idle-timeout", 0, "HTTP idle timeout, e.g. 60s; 0 uses configuration")
	shutdownTimeout := fs.Duration("shutdown-timeout", 0, "Graceful shutdown timeout; 0 uses configuration")
	shutdownAfter := fs.Duration("shutdown-after", 0, "Automatically stop server after duration, e.g. 30m; 0 disables")
	checkConfig := fs.Bool("check-config", false, "Validate configuration and exit without starting listeners")
	printVersion := fs.Bool("version", false, "Print version and exit")
	generateOpenAPI := fs.String("generate-openapi", "", "Generate a .mock DSL file from a Swagger/OpenAPI JSON or YAML document")
	generateOpenAPIOut := fs.String("generate-openapi-out", "", "Output .mock file path for -generate-openapi; use - for stdout")
	openAPIVersion := fs.String("openapi-version", "auto", "Expected specification family: auto, 2.0, 3.0, 3.1, or 3.2")
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	if *printVersion {
		fmt.Println(version)
		return
	}
	if *generateOpenAPI != "" {
		out := *generateOpenAPIOut
		if out == "" {
			base := strings.TrimSuffix(*generateOpenAPI, filepath.Ext(*generateOpenAPI))
			out = base + ".mock"
		}
		report, err := openapi.GenerateFile(*generateOpenAPI, out, *openAPIVersion)
		if err != nil {
			fmt.Fprintln(os.Stderr, "OpenAPI generation error:", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Generated %d operation(s) from %s (version %s) to %s\n", report.Operations, *generateOpenAPI, report.Version, out)
		for _, warning := range report.Warnings {
			fmt.Fprintln(os.Stderr, "warning:", warning)
		}
		return
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "configuration error:", err)
		os.Exit(1)
	}
	overrides := config.CLIOverrides{Listen: *listen, AdminListen: *adminListen, FixturesDir: *fixturesDir, LogLevel: *logLevel, LogFormat: *logFormat, Strict: *strict, NoStrict: *noStrict, MaxBodyBytes: *maxBody, ReadTimeout: *readTimeout, WriteTimeout: *writeTimeout, IdleTimeout: *idleTimeout, ShutdownTimeout: *shutdownTimeout}
	cfg.ApplyCLI(overrides)
	if err := cfg.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, "configuration error:", err)
		os.Exit(1)
	}
	if *checkConfig {
		fmt.Println("configuration is valid")
		return
	}

	logger, closeLog, err := newLogger(cfg.Logging.Level, cfg.Logging.Format, *logFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "logging initialization error:", err)
		os.Exit(1)
	}
	defer closeLog()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a, err := app.New(cfg, logger, version)
	if err != nil {
		logger.Error("initialize server", "error", err)
		os.Exit(1)
	}
	a.SetConfigReloader(func() (*config.Config, error) {
		next, err := config.Load(*configPath)
		if err != nil {
			return nil, err
		}
		next.ApplyCLI(overrides)
		if err := next.Validate(); err != nil {
			return nil, err
		}
		return next, nil
	})
	shutdown := time.Duration(cfg.Server.ShutdownTimeout)
	if shutdown <= 0 {
		shutdown = 10 * time.Second
	}
	if err := a.Run(ctx, shutdown, *shutdownAfter); err != nil {
		logger.Error("server stopped with error", "error", err)
		os.Exit(1)
	}
}

func newLogger(level, format, logPath string) (*slog.Logger, func(), error) {
	var l slog.Level
	switch strings.ToLower(level) {
	case "debug":
		l = slog.LevelDebug
	case "warn", "warning":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}
	var output io.Writer = os.Stdout
	var file *os.File
	if strings.TrimSpace(logPath) != "" {
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, func() {}, fmt.Errorf("open log file %q: %w", logPath, err)
		}
		file = f
		output = io.MultiWriter(os.Stdout, file)
	}
	opts := &slog.HandlerOptions{Level: l}
	var handler slog.Handler
	if strings.EqualFold(format, "json") {
		handler = slog.NewJSONHandler(output, opts)
	} else {
		handler = slog.NewTextHandler(output, opts)
	}
	closeFn := func() {
		if file != nil {
			_ = file.Sync()
			_ = file.Close()
		}
	}
	return slog.New(handler), closeFn, nil
}
