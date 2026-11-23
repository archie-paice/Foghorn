//go:build windows

package main

import "errors"

// execProgram is only used in Linux containers.
func execProgram(path string, argv []string) error {
	return errors.New("not supported on Windows")
}
