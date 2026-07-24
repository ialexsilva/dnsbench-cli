package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

func main() {
	root := newRootCmd()
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error: "+err.Error())
		if isUsageError(err) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

func isUsageError(err error) bool {
	var ue usageErr
	if errors.As(err, &ue) {
		return true
	}
	msg := err.Error()
	return strings.HasPrefix(msg, "unknown command") ||
		strings.HasPrefix(msg, "accepts ") ||
		strings.HasPrefix(msg, "requires at least") ||
		strings.HasPrefix(msg, "required flag")
}
