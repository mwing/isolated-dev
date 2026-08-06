// Command dev2 is the Go implementation of isolated-dev.
package main

import (
	"os"

	"github.com/mwing/isolated-dev/go/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:]))
}
