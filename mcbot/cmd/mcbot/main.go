package main

import (
	"io"
	"os"

	"github.com/snowy/mcbot/internal/cli"
)

func main() {
	os.Exit(runMain(os.Args[1:], os.Stdout, os.Stderr))
}

func runMain(args []string, stdout, stderr io.Writer) int {
	return cli.Run(args, stdout, stderr)
}
