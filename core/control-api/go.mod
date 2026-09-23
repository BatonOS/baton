module github.com/batonos/baton/core/control-api

go 1.24

require (
	github.com/coder/websocket v1.8.15
	github.com/google/uuid v1.6.0
	github.com/batonos/baton/core/pkg v0.0.0
	gopkg.in/yaml.v3 v3.0.1
	modernc.org/sqlite v1.34.4
)

require (
	github.com/dustin/go-humanize v1.0.1
	github.com/hashicorp/golang-lru/v2 v2.0.7
	github.com/mattn/go-isatty v0.0.20
	github.com/ncruces/go-strftime v0.1.9
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec
	golang.org/x/sys v0.22.0
	modernc.org/gc/v3 v3.0.0-20240107210532-573471604cb6
	modernc.org/libc v1.55.3
	modernc.org/mathutil v1.6.0
	modernc.org/memory v1.8.0
	modernc.org/strutil v1.2.0
	modernc.org/token v1.1.0
)

replace github.com/batonos/baton/core/pkg => ../pkg
