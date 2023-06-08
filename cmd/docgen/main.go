// Package main is entrypoint to tool allowing to generate content for README.md template
package main

import (
	"flag"
	"os"
	"text/template"

	"github.com/spf13/cobra/doc"
	"go.interactor.dev/terradep/cmd/cli/commands"
)

const (
	userRWX  = 0o700
	userRW   = 0o600
	cmdMdDir = "./doc/cmd"
)

func main() {
	var (
		version = ""
		url     = ""
	)
	flag.StringVar(&version, "version", "", "version")
	flag.StringVar(&url, "url", "", "name url")
	flag.Parse()

	println("version: " + version)
	println("url: " + url)

	must(os.MkdirAll(cmdMdDir, os.ModeDir|userRWX))
	must(doc.GenMarkdownTree(commands.NewCommand(), cmdMdDir))

	tpl, err := template.ParseFiles("README.md.tmpl")
	must(err)

	out, err := os.OpenFile("README.md", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, userRW)
	must(err)

	data := app{
		Name:      commands.CLIName,
		Version:   version,
		NameURL:   url,
		UsageDir:  cmdMdDir,
		UsageFile: commands.CLIName + "_graph.md",
	}

	err = tpl.Execute(out, data)
	must(err)
}

type app struct {
	Name      string
	Version   string
	NameURL   string
	UsageDir  string
	UsageFile string
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
