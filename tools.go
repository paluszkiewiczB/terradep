//go:build tools
// +build tools

package terradep

import (
	_ "github.com/editorconfig-checker/editorconfig-checker/cmd/editorconfig-checker"
	_ "github.com/golangci/golangci-lint/cmd/golangci-lint"
	_ "golang.org/x/tools/cmd/godoc"
	_ "mvdan.cc/gofumpt"
)
