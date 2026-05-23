package main

import (
	"os"

	"file-system/internal/pfs"
)

func main() {
	os.Exit(pfs.Run(os.Args[1:]))
}
