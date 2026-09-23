// SPDX-License-Identifier: Apache-2.0






package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/batonos/baton/core/control-api/internal/config"
	"github.com/batonos/baton/core/control-api/internal/core"
	_ "github.com/batonos/baton/core/control-api/internal/edition/personal"
	"github.com/batonos/baton/core/control-api/internal/server"
)

var version = "dev"


const (
	exitOK           = 0
	exitConfigError  = 2
	exitPrecondition = 3
	exitUnreachable  = 4
	exitInternal     = 10
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(exitConfigError)
	}

	switch os.Args[1] {
	case "serve":
		os.Exit(runServe(os.Args[2:]))
	case "healthcheck":
		os.Exit(runHealthcheck(os.Args[2:]))
	case "bootstrap-admin":
		os.Exit(runBootstrapAdmin(os.Args[2:]))
	case "export-transaction":
		os.Exit(runExportTransaction(os.Args[2:]))
	case "config":
		os.Exit(runConfig(os.Args[2:]))
	case "version":
		fmt.Println(version)
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "control-api: unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(exitConfigError)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `control-api - BATON control plane

  serve            run the control plane
  export-transaction  write one transaction's record to disk (server may be down)
  healthcheck      probe readiness over loopback (used by the container HEALTHCHECK)
  bootstrap-admin  issue the first operator certificate
  config validate  check a configuration file and print the effective config
  version          print the version

Configuration comes from flags, then environment (BATON_*), then a config file.
`)
}

func newLogger(cfg config.Config) *slog.Logger {
	level := slog.LevelInfo
	switch cfg.Spec.Logging.Level {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	opts := &slog.HandlerOptions{Level: level}



	var handler slog.Handler = slog.NewJSONHandler(os.Stdout, opts)
	if cfg.Spec.Logging.Format == "text" {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}
	return slog.New(handler).With("component", "control-api", "node", cfg.Metadata.Name)
}

func runServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	configPath := fs.String("config", os.Getenv("BATON_CONFIG"), "path to baton.yaml")
	_ = fs.Parse(args)

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitConfigError
	}

	logger := newLogger(cfg)



	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	srv, err := server.New(ctx, cfg, logger, version)
	if err != nil {
		logger.Error("startup failed", "error", err)
		return exitPrecondition
	}

	if err := srv.Run(ctx); err != nil {
		logger.Error("server stopped", "error", err)
		return exitInternal
	}
	logger.Info("stopped cleanly")
	return exitOK
}






func runHealthcheck(args []string) int {
	fs := flag.NewFlagSet("healthcheck", flag.ExitOnError)
	configPath := fs.String("config", os.Getenv("BATON_CONFIG"), "path to baton.yaml")
	timeout := fs.Duration("timeout", 3*time.Second, "probe timeout")
	_ = fs.Parse(args)

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitConfigError
	}

	_, port, err := splitHostPort(cfg.Spec.ListenAddress)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitConfigError
	}

	client := &http.Client{
		Timeout: *timeout,
		Transport: &http.Transport{



			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	resp, err := client.Get(fmt.Sprintf("https://127.0.0.1:%s/readyz", port))
	if err != nil {
		fmt.Fprintf(os.Stderr, "not ready: %v\n", err)
		return exitUnreachable
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var body map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&body)
		fmt.Fprintf(os.Stderr, "not ready: %s %v\n", resp.Status, body)
		return exitPrecondition
	}
	return exitOK
}





func runBootstrapAdmin(args []string) int {
	fs := flag.NewFlagSet("bootstrap-admin", flag.ExitOnError)
	configPath := fs.String("config", os.Getenv("BATON_CONFIG"), "path to baton.yaml")
	outDir := fs.String("out-dir", "", "directory for the admin certificate (default: <data-dir>/admin)")
	_ = fs.Parse(args)

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitConfigError
	}
	logger := newLogger(cfg)

	dir := *outDir
	if dir == "" {
		dir = cfg.AdminDir()
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		logger.Error("create admin directory", "error", err)
		return exitInternal
	}

	srv, err := server.New(context.Background(), cfg, logger, version)
	if err != nil {
		logger.Error("open control plane state", "error", err)
		return exitPrecondition
	}
	defer srv.Close()

	certPEM, keyPEM, err := srv.AdminCertificate(context.Background())
	if err != nil {
		logger.Error("issue admin certificate", "error", err)
		return exitInternal
	}

	certPath := filepath.Join(dir, "admin.crt")
	keyPath := filepath.Join(dir, "admin.key")
	caPath := filepath.Join(dir, "ca.crt")

	for _, f := range []struct {
		path string
		data []byte
		mode os.FileMode
	}{
		{certPath, certPEM, 0o600},
		{keyPath, keyPEM, 0o600},
		{caPath, srv.CABundle(), 0o644},
	} {
		if err := os.WriteFile(f.path, f.data, f.mode); err != nil {
			logger.Error("write credential", "path", f.path, "error", err)
			return exitInternal
		}
		if err := os.Chmod(f.path, f.mode); err != nil {
			logger.Error("set credential mode", "path", f.path, "error", err)
			return exitInternal
		}
	}


	out, _ := json.MarshalIndent(map[string]string{
		"admin_certificate": certPath,
		"admin_key":         keyPath,
		"ca_bundle":         caPath,
	}, "", "  ")
	fmt.Println(string(out))
	return exitOK
}

func runConfig(args []string) int {
	if len(args) == 0 || args[0] != "validate" {
		fmt.Fprintln(os.Stderr, "usage: control-api config validate [--config path]")
		return exitConfigError
	}
	fs := flag.NewFlagSet("config validate", flag.ExitOnError)
	configPath := fs.String("config", os.Getenv("BATON_CONFIG"), "path to baton.yaml")
	_ = fs.Parse(args[1:])

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitConfigError
	}



	rendered, err := cfg.Redacted()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitInternal
	}
	fmt.Print(rendered)
	return exitOK
}

func splitHostPort(addr string) (host, port string, err error) {
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			return addr[:i], addr[i+1:], nil
		}
	}
	return "", "", errors.New("listen address must be host:port, got " + addr)
}













func runExportTransaction(args []string) int {
	fs := flag.NewFlagSet("export-transaction", flag.ExitOnError)
	configPath := fs.String("config", os.Getenv("BATON_CONFIG"), "path to baton.yaml")
	txID := fs.String("tx", "", "transaction id to export (required)")
	outDir := fs.String("out-dir", "", "directory to write into (required)")
	_ = fs.Parse(args)

	if *txID == "" || *outDir == "" {
		fmt.Fprintln(os.Stderr, "export-transaction: --tx and --out-dir are both required")
		return exitConfigError
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitConfigError
	}
	logger := newLogger(cfg)
	srv, err := server.New(context.Background(), cfg, logger, version)
	if err != nil {
		logger.Error("open control plane state", "error", err)
		return exitPrecondition
	}
	defer srv.Close()

	written, err := srv.ExportTransaction(context.Background(), *txID, *outDir)
	if err != nil {
		logger.Error("export transaction", "transaction", *txID, "error", err)
		return exitInternal
	}



	fmt.Printf("exported %s to %s: %s\n", *txID, *outDir, strings.Join(written, " "))
	for _, item := range core.ExportedItems {
		if !slices.Contains(written, item) {
			fmt.Printf("NOT exported: %s\n", item)
		}
	}
	return 0
}
