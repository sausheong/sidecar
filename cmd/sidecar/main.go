package main

import (
	"os"

	"github.com/sausheong/sidecar/internal/cli"
)

func main() {
	if err := cli.RootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}
