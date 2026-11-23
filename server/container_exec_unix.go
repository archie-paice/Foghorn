//go:build !windows

package main

import (
	"os"
	"syscall"
)

// execProgram replaces this process with path, keeping its PID, so Docker
// (or systemd) sees one process throughout.
func execProgram(path string, argv []string) error {
	return syscall.Exec(path, argv, os.Environ())
}
