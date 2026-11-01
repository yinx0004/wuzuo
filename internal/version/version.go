// SPDX-License-Identifier: Apache-2.0

// Package version holds build metadata injected via -ldflags.
package version

// Set by GoReleaser: -X github.com/yinx0004/yuanfang/internal/version.Version=...
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)
