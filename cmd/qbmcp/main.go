package main

import (
	"fmt"
	"os"

	"qbmcp/internal/cli"
)

func main() {
	if err := cli.Run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "qbmcp:", err)
		os.Exit(1)
	}
}
