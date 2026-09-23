// SPDX-License-Identifier: Apache-2.0





package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)


const APIVersion = "baton.mailloop.dev/v1alpha1"


type Mode string

const (

	ModePrimary Mode = "primary"


	ModeMirror Mode = "mirror"
)


type Config struct {
	APIVersion string   `yaml:"apiVersion"`
	Kind       string   `yaml:"kind"`
	Metadata   Metadata `yaml:"metadata"`
	Spec       Spec     `yaml:"spec"`
}


type Metadata struct {
	Name string `yaml:"name"`
}


type Spec struct {



	WithAgentRole bool `yaml:"withAgentRole"`
	Mode    Mode   `yaml:"mode"`
	Edition string `yaml:"edition"`

	DataDir      string `yaml:"dataDir"`
	ListenAddress string `yaml:"listenAddress"`
	AdvertiseURL string `yaml:"advertiseURL"`




	ControlEndpoints []string `yaml:"controlEndpoints,omitempty"`







	TLSSANs []string `yaml:"tlsSANs,omitempty"`


	MasterURL string `yaml:"masterURL,omitempty"`

	SnapshotIntervalSec int `yaml:"snapshotIntervalSec,omitempty"`

	Metrics MetricsSpec `yaml:"metrics"`
	Logging LoggingSpec `yaml:"logging"`




	RetentionDays int `yaml:"retentionDays"`





	ProviderSocket string `yaml:"providerSocket,omitempty"`














	WorkspaceBoundary string `yaml:"workspaceBoundary,omitempty"`
}


type MetricsSpec struct {



	Enabled bool   `yaml:"enabled"`
	Address string `yaml:"address"`
}


type LoggingSpec struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}


func Default() Config {
	return Config{
		APIVersion: APIVersion,
		Kind:       "BatonNode",
		Metadata:   Metadata{Name: "master01"},
		Spec: Spec{
			Mode:          ModePrimary,
			Edition:       "personal",
			DataDir:       "/var/lib/baton",
			ListenAddress: "0.0.0.0:8443",
			AdvertiseURL:  "https://127.0.0.1:8443",
			SnapshotIntervalSec: 60,
			Metrics: MetricsSpec{
				Enabled: false,


				Address: "127.0.0.1:9090",
			},
			Logging:       LoggingSpec{Level: "info", Format: "json"},
			RetentionDays: 30,
		},
	}
}



func Load(path string) (Config, error) {
	cfg := Default()

	if path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return cfg, fmt.Errorf("config: read %s: %w", path, err)
		}



		dec := yaml.NewDecoder(strings.NewReader(string(raw)))
		dec.KnownFields(true)
		if err := dec.Decode(&cfg); err != nil {
			return cfg, fmt.Errorf("config: parse %s: %w", path, err)
		}
	}

	applyEnv(&cfg)
	return cfg, cfg.Validate()
}

func applyEnv(cfg *Config) {
	env := func(key string, set func(string)) {
		if v, ok := os.LookupEnv(key); ok && v != "" {
			set(v)
		}
	}
	env("BATON_NAME", func(v string) { cfg.Metadata.Name = v })
	env("BATON_WITH_AGENT_ROLE", func(v string) { cfg.Spec.WithAgentRole = v == "true" })
	env("BATON_MODE", func(v string) { cfg.Spec.Mode = Mode(v) })
	env("BATON_EDITION", func(v string) { cfg.Spec.Edition = v })
	env("BATON_DATA_DIR", func(v string) { cfg.Spec.DataDir = v })
	env("BATON_LISTEN_ADDRESS", func(v string) { cfg.Spec.ListenAddress = v })
	env("BATON_ADVERTISE_URL", func(v string) { cfg.Spec.AdvertiseURL = v })
	env("BATON_MASTER_URL", func(v string) { cfg.Spec.MasterURL = v })
	env("BATON_TLS_SANS", func(v string) {
		for _, name := range strings.Split(v, ",") {
			if name = strings.TrimSpace(name); name != "" {
				cfg.Spec.TLSSANs = append(cfg.Spec.TLSSANs, name)
			}
		}
	})
	env("BATON_PROVIDER_SOCKET", func(v string) { cfg.Spec.ProviderSocket = v })
	env("BATON_WORKSPACE_BOUNDARY", func(v string) { cfg.Spec.WorkspaceBoundary = v })
	env("BATON_LOG_LEVEL", func(v string) { cfg.Spec.Logging.Level = v })
	env("BATON_METRICS_ADDRESS", func(v string) { cfg.Spec.Metrics.Address = v })
	env("BATON_METRICS_ENABLED", func(v string) {
		cfg.Spec.Metrics.Enabled = v == "1" || strings.EqualFold(v, "true")
	})
	env("BATON_RETENTION_DAYS", func(v string) {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Spec.RetentionDays = n
		}
	})
	env("BATON_SNAPSHOT_INTERVAL_SEC", func(v string) {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Spec.SnapshotIntervalSec = n
		}
	})
}






func (c Config) Validate() error {
	if c.APIVersion != APIVersion {
		return fmt.Errorf("config: apiVersion is %q, this build understands %q",
			c.APIVersion, APIVersion)
	}
	if c.Kind != "BatonNode" {
		return fmt.Errorf("config: kind is %q, want BatonNode", c.Kind)
	}
	if c.Metadata.Name == "" {
		return fmt.Errorf("config: metadata.name is required")
	}
	switch c.Spec.Mode {
	case ModePrimary, ModeMirror:
	default:
		return fmt.Errorf("config: spec.mode is %q, want primary or mirror", c.Spec.Mode)
	}
	if c.Spec.Mode == ModeMirror && c.Spec.MasterURL == "" {
		return fmt.Errorf("config: spec.masterURL is required in mirror mode — a mirror has to know what to mirror")
	}
	if c.Spec.DataDir == "" {
		return fmt.Errorf("config: spec.dataDir is required")
	}
	if c.Spec.AdvertiseURL != "" {
		u, err := url.Parse(c.Spec.AdvertiseURL)
		if err != nil || u.Scheme != "https" {
			return fmt.Errorf("config: spec.advertiseURL must be an https:// URL, got %q", c.Spec.AdvertiseURL)
		}
	}
	if c.Spec.RetentionDays < 0 {
		return fmt.Errorf("config: spec.retentionDays cannot be negative")
	}
	return nil
}



func (c Config) DBPath() string     { return filepath.Join(c.Spec.DataDir, "baton.db") }
func (c Config) CADir() string      { return filepath.Join(c.Spec.DataDir, "ca") }



func (c Config) AppsDir() string { return filepath.Join(c.Spec.DataDir, "apps") }
func (c Config) TLSDir() string     { return filepath.Join(c.Spec.DataDir, "tls") }
func (c Config) AdminDir() string   { return filepath.Join(c.Spec.DataDir, "admin") }
func (c Config) SnapshotDir() string { return filepath.Join(c.Spec.DataDir, "snapshots") }













func (c Config) BlobDir() string { return filepath.Join(c.Spec.DataDir, "blobs") }


func (c Config) SnapshotInterval() time.Duration {
	if c.Spec.SnapshotIntervalSec <= 0 {
		return 60 * time.Second
	}
	return time.Duration(c.Spec.SnapshotIntervalSec) * time.Second
}


func (c Config) ReadOnly() bool { return c.Spec.Mode == ModeMirror }







func (c Config) Redacted() (string, error) {
	out, err := yaml.Marshal(c)
	if err != nil {
		return "", err
	}
	return string(out), nil
}
