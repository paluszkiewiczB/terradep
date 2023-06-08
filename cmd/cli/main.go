// package main is entrypoint to terradep cli application
package main

import (
	"fmt"
	"os"

	"go.interactor.dev/terradep/cmd/cli/commands"
)

func main() {
	command := commands.NewCommand()
	if err := command.Execute(); err != nil {
		fmt.Printf("terradep failed: %s\n", err)
		os.Exit(1)
	}
}
