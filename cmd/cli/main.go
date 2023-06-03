// package main is entrypoint to terradep cli application
package main

import (
	"log"
	"os"

	"go.interactor.dev/terradep/state"

	"go.interactor.dev/terradep/encoding"

	"go.interactor.dev/terradep"
)

const (
	userRW               = 0o600
	argLenWithOutputFile = 3
)

func main() {
	path := os.Args[1]
	log.Printf("analyzing: %s", path)

	stater := state.NewByTypeStater(map[string]terradep.Stater{
		state.S3Backend: state.NewS3Stater(state.WithS3Region(), state.WithS3Encryption()),
	})

	s := terradep.NewScanner(stater)
	graph, err := s.Scan(path)
	if err != nil {
		log.Printf("failed to scan path: %s, error was: %s", path, err)
		os.Exit(1)
	}

	log.Printf("scan successful, graph: %v", graph)

	encoded, err := encoding.BuildDOTGraph(graph)
	if err != nil {
		log.Printf("failed to encode the graph, %s", err)
	}
	if len(os.Args) < argLenWithOutputFile {
		log.Print(string(encoded))
		return
	}

	outFile := os.Args[2]
	err = os.WriteFile(outFile, encoded, userRW)
	if err != nil {
		log.Printf("failed to write output to file: %s", err)
		os.Exit(1)
	}
}
