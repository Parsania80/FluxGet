package main

import (
	"github.com/Parsania80/FluxGet/internal/engine"
	"os"
)

func main() {
	os.Exit(engine.Run(os.Args[1:], os.Stdout, os.Stderr))
}
