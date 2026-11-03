// Command latprobe measures network latency to many targets.
package main

import (
	"os"

	"github.com/uu889/latprobe/internal/cli"
)

// version is set at build time: -ldflags "-X main.version=v0.1.0".
var version = "dev"

func main() {
	os.Exit(cli.Execute(version))
}
