package main

import (
	"os"

	dashboard "github.com/artur-faizov/token-monitor/src/dashboard"
)

func main() {
	os.Exit(dashboard.Run(os.Args[1:]))
}
