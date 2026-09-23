module github.com/batonos/baton/core/agent

go 1.24

require (
	github.com/coder/websocket v1.8.12
	github.com/google/uuid v1.6.0
	gopkg.in/yaml.v3 v3.0.1
)

replace github.com/batonos/baton/core/pkg => ../pkg
