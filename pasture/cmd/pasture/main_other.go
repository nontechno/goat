//go:build !unix

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "pasture needs a Unix system (Linux, macOS, BSD); on Windows, run it in WSL")
	os.Exit(1)
}
