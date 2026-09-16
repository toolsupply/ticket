// Command ticket is the thin process entry point for the ticket CLI.
package main

import (
	"os"

	"ticket/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout))
}
