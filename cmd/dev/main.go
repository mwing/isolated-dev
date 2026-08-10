// Command dev is the Go implementation of isolated-dev.
package main

import (
	"os"

	"github.com/mwing/isolated-dev/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:]))
}
