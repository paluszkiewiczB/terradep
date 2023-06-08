// package main is entrypoint to terradep cli application
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"golang.org/x/exp/slog"

	"github.com/spf13/cobra"

	"go.interactor.dev/terradep/state"

	"go.interactor.dev/terradep/encoding"

	"go.interactor.dev/terradep"
)

const (
	// it is illegal name of the file, so if this value will not be handled properly, application should blow up
	defaultLogFile = string(os.PathSeparator)
	userRW         = 0o600
	cliName        = "terradep"
)

// version is expected to be set with -ldflags="-X main.version=1.2.3"
var version = "dev-version"

type rootCfg struct {
	dryRun   bool
	quiet    bool
	logLevel string
	logFmt   string
	logFile  string
}

type graphCfg struct {
	*rootCfg
	dirs    []string
	outFile string
	force   bool
	// TODO support log levels, use slog
}

func main() {
	command := NewCommand()
	if err := command.Execute(); err != nil {
		fmt.Printf("terradep failed: %s\n", err)
		os.Exit(1)
	}
}

func NewCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:     cliName + " [--dry run] [--log-format (TEXT|JSON)] [--log-level (DEBUG|INFO|WARN|ERROR)] [--log-file[=fileName.log]] <subCommand>",
		Example: cliName + " graph",
		Short:   cliName + " is cli tool which generates dependency graph of Terraform deployments",
		Version: version,
	}

	rc := &rootCfg{}
	rF := rootCmd.PersistentFlags()
	rF.BoolVar(&rc.dryRun, "dry-run", false, "Does not produce the output when enabled. Can be used as a 'linter' for the input")
	rF.BoolVarP(&rc.quiet, "quiet", "q", false, "Does not produce logs when enabled. Overrides log-level.")
	rF.StringVar(&rc.logLevel, "log-level", "INFO", "Sets log level. Ignored when --quiet was used.")
	rF.StringVar(&rc.logFile, "log-file", "", "Writes logs to specified file. If file does not exist - creates it, otherwise appends to existing one. When flag is set without parameter, name of the file is generated based on current time. If not set logs are written to standard error")
	rF.Lookup("log-file").NoOptDefVal = defaultLogFile
	rF.StringVar(&rc.logFmt, "log-format", "TEXT", "Sets log format. Allowed values: TEXT, JSON")

	gc := &graphCfg{rootCfg: rc}
	graphCmd := &cobra.Command{
		Use:     `graph [--force] [--out fileName.dot] --dir analyzeMe`,
		Example: `graph --log-file --dir analyzeMe > graph.dot`,
		Short:   "Builds dependency grap. Reads from directory analyzeMe and writes to stdout which is redirected to graph.dot. Logs are written to automatically created file",
		RunE:    generateGraph(gc),
	}

	gF := graphCmd.Flags()
	gF.StringSliceVarP(&gc.dirs, "dir", "d", nil, "Recursively analyzes specified directories.")
	gF.StringVarP(&gc.outFile, "out", "o", "", "Writes output to specified file. Fails when file already exists unless you set flag --force")
	gF.BoolVarP(&gc.force, "force", "f", false, "Writes output to file specified with --out even if it already exists. Existing file content WILL BE LOST")

	err := graphCmd.MarkFlagRequired("dir")
	if err != nil {
		panic(fmt.Errorf("marking flag dir as required, %w", err))
	}
	rootCmd.AddCommand(graphCmd)
	return rootCmd
}

func generateGraph(c *graphCfg) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		log, err := buildLogger(*c.rootCfg)
		if err != nil {
			return fmt.Errorf("failed to build logger: %s", err)
		}

		if len(c.dirs) == 0 {
			return fmt.Errorf("no directories to scan")
		}

		out, err := buildOutput(log, c)
		if err != nil {
			return fmt.Errorf("building output: %w", err)
		}

		stater := state.NewByTypeStater(map[string]terradep.Stater{
			state.S3Backend: state.NewS3Stater(state.WithS3Region(), state.WithS3Encryption()),
		})

		s := terradep.NewScanner(log, stater)
		graphs := make([]*terradep.Graph, len(c.dirs))
		for i, dir := range c.dirs {
			log.Info("scanning directory", slog.String("dir", dir))
			graph, err := s.Scan(dir)
			if err != nil {
				return fmt.Errorf("failed to scan path: %s, error was: %w", dir, err)
			}
			graphs[i] = graph
		}

		graph, err := terradep.MergeGraphs(log, graphs...)
		if err != nil {
			return fmt.Errorf("failed to merge graphs, error was: %w", err)
		}

		log.Info("scan successful", slog.Any("graph", graph))

		encoded, err := encoding.BuildDOTGraph(graph)
		if err != nil {
			log.Error("failed to encode the graph", err)
		}

		n, err := out.Write(encoded)
		if err != nil {
			return fmt.Errorf("failed to write dot graph to output: %s, written: %d bytes, %w", out, n, err)
		}

		return nil
	}
}

func buildOutput(log *slog.Logger, c *graphCfg) (io.Writer, error) {
	if c.dryRun {
		return io.Discard, nil
	}

	if len(c.outFile) == 0 {
		return os.Stderr, nil
	}

	_, err := os.Stat(c.outFile)
	if errors.Is(err, os.ErrNotExist) {
		log.Debug("output file does not exist", slog.String("created", c.outFile))
		file, err := os.Create(c.outFile)
		if err != nil {
			return nil, fmt.Errorf("creating output file: %s, %w", c.outFile, err)
		}
		return file, nil
	} else if err != nil {
		// unexpected error
		return nil, fmt.Errorf("stating out file: %s, %w", c.outFile, err)
	}

	if !c.force {
		return nil, fmt.Errorf("output file already exist and force is disabled: %s", c.outFile)
	}

	log.Debug("force enabled, writing output to existing file", slog.String("path", c.outFile))
	file, err := os.OpenFile(c.outFile, os.O_RDWR|os.O_TRUNC, userRW)
	if err != nil {
		return nil, fmt.Errorf("overwriting output file: %s, %w", c.outFile, err)
	}

	return file, nil
}

func buildLogger(c rootCfg) (*slog.Logger, error) {
	defLvl := slog.LevelInfo
	lvl := &defLvl
	err := lvl.UnmarshalText([]byte(c.logLevel)) // mutates lvl
	if err != nil {
		return nil, fmt.Errorf("parsing log level: %w", err)
	}

	handlerFn, ok := handlers[c.logFmt]
	if !ok {
		return nil, fmt.Errorf("unsupported log format: %s", c.logFmt)
	}

	dst, err := buildLogDst(c)
	if err != nil {
		return nil, err
	}

	handler := handlerFn(dst, &slog.HandlerOptions{Level: lvl})
	return slog.New(handler), nil
}

var handlers = map[string]func(io.Writer, *slog.HandlerOptions) slog.Handler{
	"TEXT": func(writer io.Writer, opts *slog.HandlerOptions) slog.Handler {
		return slog.NewTextHandler(writer, opts)
	},
	"JSON": func(writer io.Writer, opts *slog.HandlerOptions) slog.Handler {
		return slog.NewJSONHandler(writer, opts)
	},
}

func buildLogDst(c rootCfg) (io.Writer, error) {
	if c.quiet {
		return io.Discard, nil
	}

	if len(c.logFile) == 0 {
		// flag not set
		return os.Stderr, nil
	}

	if c.logFile == defaultLogFile {
		// flag set without parameter
		now := time.Now()
		return os.Create(now.Format(cliName + "_grap_" + time.RFC3339Nano + ".log"))
	}

	return os.OpenFile(c.logFile, os.O_WRONLY|os.O_APPEND|os.O_CREATE, userRW)
}
