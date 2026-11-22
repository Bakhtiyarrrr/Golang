package main

import (
	"errors"
	"fmt"
	"io"
	"os"
)

func main() {
	if err := runCLI(os.Stdin, os.Stdout); err != nil && !errors.Is(err, io.EOF) {
		fmt.Fprintln(os.Stderr, "Error:", err)
	}
}
