// SPDX-License-Identifier: Apache-2.0






package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/batonos/baton/core/agent/internal/agent"
	"github.com/batonos/baton/core/agent/internal/identity"
)

var version = "dev"


const (
	exitOK           = 0
	exitConfigError  = 2
	exitPrecondition = 3
	exitAuth         = 5
	exitInternal     = 10
)

func main() {











	switch filepath.Base(os.Args[0]) {
	case "baton-inbox":
		os.Exit(runInbox(os.Args[1:]))
	case "baton-resource":
		os.Exit(runResource(os.Args[1:]))
	case "baton-plugin":
		os.Exit(runPlugin(os.Args[1:]))
	}

	if len(os.Args) < 2 {
		usage()
		os.Exit(exitConfigError)
	}

	switch os.Args[1] {
	case "run":
		os.Exit(runAgent(os.Args[2:]))
	case "enroll":
		os.Exit(runEnroll(os.Args[2:]))
	case "apply":
		os.Exit(runApply(os.Args[2:]))
	case "healthcheck":
		os.Exit(runHealthcheck(os.Args[2:]))
	case "inbox":
		os.Exit(runInbox(os.Args[2:]))
	case "shell":
		os.Exit(runShell(os.Args[2:]))
	case "version":
		fmt.Println(version)
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "baton-agent: unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(exitConfigError)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `baton-agent - BATON node daemon

  apply        ask to join a network — this node's name and public key, nothing else
  enroll       redeem an enrollment token and store this node's identity
  run          supervise this node's runtime and serve the control plane channel
  healthcheck  report whether this node is enrolled and not revoked
  inbox        take this node's mail — the runtime asks, nothing pushes
  shell on|off allow or refuse the operator's remote shell (a per-node switch, off by default, enforced by this node); no arg prints it
  version      print the version

Environment: BATON_DATA_DIR, BATON_MASTER_URL, BATON_NODE_NAME, BATON_LABELS,
             BATON_ALLOW_REMOTE_SHELL (create-time default for shell),
             BATON_ENROLLMENT_TOKEN_FILE, BATON_CAPABILITIES,
             BATON_RUNTIME_SPEC

A token is read from a file or from BATON_ENROLLMENT_TOKEN. It is never
accepted as a command-line flag, where it would land in shell history and in
the process list.
`)
}

type commonFlags struct {
	dataDir     string
	masterURL   string
	name        string
	manifest    string
	runtimeSpec string
	logLevel    string
}

func bindCommon(fs *flag.FlagSet) *commonFlags {
	c := &commonFlags{}
	fs.StringVar(&c.dataDir, "data-dir", envOr("BATON_DATA_DIR", "/var/lib/baton"),
		"persistent data directory")
	fs.StringVar(&c.masterURL, "master", os.Getenv("BATON_MASTER_URL"),
		"control plane URL, e.g. https://baton.example.com:8443")
	fs.StringVar(&c.name, "name", os.Getenv("BATON_NODE_NAME"), "this node's display name")
	fs.StringVar(&c.manifest, "capabilities", os.Getenv("BATON_CAPABILITIES"),
		"path to capabilities.yaml")
	fs.StringVar(&c.runtimeSpec, "runtime-spec",
		envOr("BATON_RUNTIME_SPEC", "/etc/baton/runtime.yaml"),
		"path to the AgentRuntime spec; a missing file means this node supervises nothing")
	fs.StringVar(&c.logLevel, "log-level", envOr("BATON_LOG_LEVEL", "info"),
		"debug, info, warn, or error")
	return c
}

func agentConfig(c *commonFlags, logger *slog.Logger) agent.Config {
	return agent.Config{
		DataDir:         c.dataDir,
		MasterURL:       c.masterURL,
		Name:            c.name,
		Version:         version,
		ManifestPath:    c.manifest,
		RuntimeSpecPath: c.runtimeSpec,
		Logger:          logger,
	}
}

func newLogger(level, name string) *slog.Logger { return loggerTo(os.Stdout, level, name) }







func loggerTo(w *os.File, level, name string) *slog.Logger {
	lv := slog.LevelInfo
	switch level {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	}
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: lv})).
		With("component", "agent", "node", name)
}

func runEnroll(args []string) int {
	fs := flag.NewFlagSet("enroll", flag.ExitOnError)
	common := bindCommon(fs)
	tokenFile := fs.String("enrollment-token-file",
		os.Getenv("BATON_ENROLLMENT_TOKEN_FILE"), "file containing the enrollment token")
	roles := fs.String("roles", "agent", "comma-separated: master, agent, or both")
	labels := fs.String("labels", "", "comma-separated key=value labels")
	_ = fs.Parse(args)

	if common.masterURL == "" || common.name == "" {
		fmt.Fprintln(os.Stderr, "enroll: --master and --name are required")
		return exitConfigError
	}

	token, err := readToken(*tokenFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitConfigError
	}


	logger := loggerTo(os.Stderr, common.logLevel, common.name)
	a := agent.New(agentConfig(common, logger))

	if err := a.Enroll(context.Background(), token, splitRoles(*roles), parseLabels(*labels)); err != nil {



		fmt.Fprintln(os.Stderr, "enrollment failed:", err)
		return exitAuth
	}

	id, err := identity.NewStore(common.dataDir).Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitInternal
	}
	out, _ := json.MarshalIndent(map[string]any{
		"node_id":           id.NodeID,
		"display_name":      id.DisplayName,
		"roles":             strings.Join(id.Roles, ","),
		"cluster_id":        id.ClusterID,
		"cert_not_after":    id.CertNotAfter,
		"control_endpoints": id.ControlEndpoints,
	}, "", "  ")
	fmt.Println(string(out))
	return exitOK
}




func runApply(args []string) int {
	fs := flag.NewFlagSet("apply", flag.ExitOnError)
	common := bindCommon(fs)
	network := fs.String("network", "", "the network's entry point, https://host:port")
	pin := fs.String("pin", "", "the network key fingerprint the entry point must prove (from DNS or a registry); empty on channel 0")
	_ = fs.Parse(args)
	if *network == "" || common.name == "" {
		fmt.Fprintln(os.Stderr, "apply: --network and --name are required")
		return exitConfigError
	}
	logger := loggerTo(os.Stderr, common.logLevel, common.name)
	a := agent.New(agentConfig(common, logger))
	app, err := a.Apply(*network, *pin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "application failed:", err)
		return exitAuth
	}
	out, _ := json.MarshalIndent(map[string]any{
		"request_id":  app.RequestID,
		"agent":       app.Agent,
		"network":     app.EntryPoint,
		"fingerprint":         app.Fingerprint,
		"network_fingerprint": app.NetworkFingerprint,
		"pinned":              *pin != "",
		"state":               "pending",
	}, "", "  ")
	fmt.Println(string(out))
	return exitOK
}

func runAgent(args []string) int {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	common := bindCommon(fs)
	tokenFile := fs.String("enrollment-token-file",
		os.Getenv("BATON_ENROLLMENT_TOKEN_FILE"),
		"enrollment token file; used only if this node is not yet enrolled")
	roles := fs.String("roles", "agent", "comma-separated: master, agent, or both")
	labels := fs.String("labels", os.Getenv("BATON_LABELS"), "comma-separated key=value labels, registered at enrolment (also BATON_LABELS)")
	_ = fs.Parse(args)








	logger := newLogger(common.logLevel, common.name)
	a := agent.New(agentConfig(common, logger))
	a.SetLabels(parseLabels(*labels))

	if err := a.LoadManifest(); err != nil {
		logger.Error("capability manifest", "error", err)
		return exitConfigError
	}





	if err := a.LoadRuntime(); err != nil {
		logger.Error("runtime spec", "error", err)
		return exitConfigError
	}



	if *tokenFile != "" || os.Getenv("BATON_ENROLLMENT_TOKEN") != "" {
		token, err := readToken(*tokenFile)
		if err != nil {
			logger.Error("read enrollment token", "error", err)
			return exitConfigError
		}
		if err := a.Enroll(context.Background(), token, splitRoles(*roles), parseLabels(*labels)); err != nil {
			logger.Error("enrollment failed", "error", err)
			return exitAuth
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := a.Run(ctx); err != nil {
		logger.Error("agent stopped", "error", err)
		return exitPrecondition
	}
	logger.Info("stopped cleanly")
	return exitOK
}











func runShell(args []string) int {
	fs := flag.NewFlagSet("shell", flag.ExitOnError)
	common := bindCommon(fs)
	_ = fs.Parse(args)

	switch fs.Arg(0) {
	case "", "status":
		if agent.RemoteShellAllowed(common.dataDir) {
			fmt.Println("on")
		} else {
			fmt.Println("off")
		}
		return exitOK
	case "on":
		if err := agent.SetRemoteShell(common.dataDir, true); err != nil {
			fmt.Fprintln(os.Stderr, "set remote shell:", err)
			return exitInternal
		}
		fmt.Println("on")
		return exitOK
	case "off":
		if err := agent.SetRemoteShell(common.dataDir, false); err != nil {
			fmt.Fprintln(os.Stderr, "set remote shell:", err)
			return exitInternal
		}
		fmt.Println("off")
		return exitOK
	default:
		fmt.Fprintf(os.Stderr, "baton-agent shell: expected on|off, got %q\n", fs.Arg(0))
		return exitConfigError
	}
}

func runHealthcheck(args []string) int {
	fs := flag.NewFlagSet("healthcheck", flag.ExitOnError)
	common := bindCommon(fs)
	_ = fs.Parse(args)

	store := identity.NewStore(common.dataDir)
	if store.Revoked() {
		fmt.Fprintln(os.Stderr, "this node has been revoked")
		return exitPrecondition
	}
	if !store.Enrolled() {
		fmt.Fprintln(os.Stderr, "not enrolled")
		return exitPrecondition
	}
	return exitOK
}






func readToken(path string) (string, error) {
	if env := os.Getenv("BATON_ENROLLMENT_TOKEN"); env != "" {
		return strings.TrimSpace(env), nil
	}
	if path == "" {
		return "", fmt.Errorf("an enrollment token is required: pass --enrollment-token-file " +
			"or set BATON_ENROLLMENT_TOKEN (never a command-line flag)")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read enrollment token from %s: %w", filepath.Clean(path), err)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", fmt.Errorf("%s is empty", path)
	}
	return token, nil
}

func parseLabels(raw string) map[string]string {
	if raw == "" {
		return nil
	}
	out := map[string]string{}
	for _, pair := range strings.Split(raw, ",") {
		if k, v, found := strings.Cut(strings.TrimSpace(pair), "="); found {
			out[k] = v
		}
	}
	return out
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}





func splitRoles(raw string) []string {
	out := []string{}
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
