package cliexit

import (
	"fmt"
	"os"
)

func ExitWithError(err error) {
	fmt.Fprintf(os.Stderr, "\033[31merror: %v\033[0m\n", err)
	os.Exit(1)
}
