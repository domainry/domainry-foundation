package main

import (
	"fmt"
	"os"

	"github.com/domainry/domainry-foundation/logging/logcontract"
)

func main() {
	roots := os.Args[1:]
	if len(roots) == 0 {
		roots = []string{"."}
	}
	violations, err := logcontract.Check(roots...)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	for _, violation := range violations {
		_, _ = fmt.Fprintln(os.Stderr, violation.String())
	}
	if len(violations) > 0 {
		os.Exit(1)
	}
}
