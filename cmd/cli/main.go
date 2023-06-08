// package main is entrypoint to terradep cli application
package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"go.interactor.dev/terradep/state"

	"go.interactor.dev/terradep/encoding"

	"go.interactor.dev/terradep"
)

const (
	// it is illegal name of the file, so if this value will not be handled properly, application should blow up
	defaultLogFile = string(os.PathSeparator)
	userRW         = 0o600
)

// version is expected to be set with -ldflags="-X main.version=1.2.3"
var version = "dev-version"

type cfg struct {
	dirs    []string
	outFile string
	logFile string
	dryRun  bool
	force   bool
	// TODO support log levels, use slog
}

func main() {
	rootCmd := &cobra.Command{
		Use:           "terradep",
		Example:       "terradep [sub]",
		Short:         "terradep is cli tool which generates dependency graph of Terraform deployments",
		Version:       version,
		SilenceErrors: true,
	}

	c := &cfg{}

	graphCmd := &cobra.Command{
		Use:     `graph [--force] [--dry run] [--log-file[ fileName.log]] [--out fileName.dot] --dir analyzeMe`,
		Example: `graph --log-file --dir analyzeMe > graph.dot`,
		Short:   "Builds dependency grap. Reads from directory analyzeMe and writes to stdout which is redirected to graph.dot. Logs are written to automatically created file",
		RunE:    generateGraph(c),
	}

	gF := graphCmd.Flags()
	gF.StringSliceVarP(&c.dirs, "dir", "d", nil, "Recursively analyzes specified directories.")
	gF.StringVarP(&c.outFile, "out", "o", "", "Writes output to specified file. Fails when file already exists unless you set flag --force")
	gF.StringVarP(&c.logFile, "log-file", "l", "", "Writes logs to specified file. When flag is set without parameter, name of the file is generated based on current time. If not set logs are written to standard error")
	gF.Lookup("log-file").NoOptDefVal = string(filepath.Separator)
	gF.BoolVar(&c.dryRun, "dry-run", false, "Does not produce the output. Can be used as a 'linter' for the input")
	gF.BoolVarP(&c.force, "force", "f", false, "Writes output to file specified with --out even if it already exists. Existing file content WILL BE LOST")

	err := graphCmd.MarkFlagRequired("dir")
	if err != nil {
		panic(fmt.Errorf("marking flag dir as required, %w", err))
	}
	rootCmd.AddCommand(graphCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Printf("terradep failed: %s\n", err)
		os.Exit(1)
	}
}

func generateGraph(c *cfg) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		if len(c.dirs) == 0 {
			return fmt.Errorf("no directories to scan")
		}

		logDst, err := buildLogDst(c)
		if err != nil {
			return err
		}
		log.SetOutput(logDst)

		out, err := buildOutput(c)
		if err != nil {
			return fmt.Errorf("building output: %w", err)
		}

		stater := state.NewByTypeStater(map[string]terradep.Stater{
			state.S3Backend: state.NewS3Stater(state.WithS3Region(), state.WithS3Encryption()),
		})

		s := terradep.NewScanner(stater)
		graphs := make([]*terradep.Graph, len(c.dirs))
		for i, dir := range c.dirs {
			log.Printf("scanning directory: %s", dir)
			graph, err := s.Scan(dir)
			if err != nil {
				return fmt.Errorf("failed to scan path: %s, error was: %w", dir, err)
			}
			graphs[i] = graph
		}

		graph, err := terradep.MergeGraphs(graphs...)
		if err != nil {
			return fmt.Errorf("failed to merge graphs, error was: %w", err)
		}

		log.Printf("scan successful, graph: %v", graph)

		encoded, err := encoding.BuildDOTGraph(graph)
		if err != nil {
			log.Printf("failed to encode the graph, %s", err)
		}

		n, err := out.Write(encoded)
		if err != nil {
			return fmt.Errorf("failed to write dot graph to output: %s, written: %d bytes, %w", out, n, err)
		}

		return nil
	}
}

func buildOutput(c *cfg) (io.Writer, error) {
	if c.dryRun {
		return io.Discard, nil
	}

	if len(c.outFile) == 0 {
		return os.Stderr, nil
	}

	_, err := os.Stat(c.outFile)
	if errors.Is(err, os.ErrNotExist) {
		log.Printf("output file does not exist, creating: %s", c.outFile)
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

	log.Printf("force enabled, writing output to existing file: %s", c.outFile)
	file, err := os.OpenFile(c.outFile, os.O_RDWR|os.O_TRUNC, userRW)
	if err != nil {
		return nil, fmt.Errorf("overwriting output file: %s, %w", c.outFile, err)
	}

	return file, nil
}

func buildLogDst(c *cfg) (io.Writer, error) {
	if len(c.logFile) == 0 {
		// flag not set
		return os.Stderr, nil
	}

	if c.logFile == defaultLogFile {
		// flag set without parameter
		now := time.Now()
		c.logFile = now.Format("terradep_grap_" + time.RFC3339Nano + ".log")
	}

	return os.Create(c.logFile)
}
