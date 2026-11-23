package main

// container.go - updating the server when it runs in Docker.
//
// The program in the image cannot be replaced: it is lost when the container
// is recreated. So "Update server" puts the new program in the data volume,
// at /data/bin/foghorn-server, and every start begins with the image's
// program asking whether that copy is newer than itself. If it is, it hands
// over to it. Rebuilding the image with a newer version still wins, because
// then the image's program is the newer one and simply carries on.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func inContainer() bool { return os.Getenv("FOGHORN_IN_CONTAINER") != "" }

// containerProgram is where an update installed from the console lives.
func containerProgram(dataDir string) string {
	name := "foghorn-server"
	if runtime.GOOS == "windows" { // only for tests; containers are Linux
		name += ".exe"
	}
	return filepath.Join(dataDir, "bin", name)
}

// newerContainerProgram returns the data volume's program if it is a newer
// version than this one and not this very program, else "".
func newerContainerProgram(dataDir string) string {
	p := containerProgram(dataDir)
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	if self, err := os.Executable(); err == nil {
		if s, err := filepath.EvalSymlinks(self); err == nil {
			self = s
		}
		if q, err := filepath.EvalSymlinks(p); err == nil && q == self {
			return "" // already running it
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, p, "version").Output()
	v := strings.TrimSpace(string(out))
	if err != nil || !validVersion(v) || !versionLess(version, v) {
		return ""
	}
	return p
}

// handOffToNewer runs the data volume's program in place of this one when it
// is newer. It only returns if there is nothing newer, or the hand-over failed.
func handOffToNewer(dataDir string) {
	p := newerContainerProgram(dataDir)
	if p == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "foghorn-server %s: handing over to the newer %s\n", version, p)
	if err := execProgram(p, append([]string{p}, os.Args[1:]...)); err != nil {
		fmt.Fprintf(os.Stderr, "foghorn-server: could not start %s, carrying on with %s: %v\n", p, version, err)
	}
}
