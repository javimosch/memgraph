//go:build unix

package cloudserver

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

// Limit runs a program and kills it, with everything it started, after a number
// of seconds: args are the seconds, the program and its arguments. The server
// runs every git through it, as a program of its own that CGI can start. It
// returns the exit code.
//
// This does not use the timeout command: the one of newer Ubuntu releases
// (uutils) leaves git's own child processes running.
func Limit(args []string) int {
	fail := func(err error) int {
		fmt.Fprintln(os.Stderr, "memgraph limit:", err)
		return 125
	}
	if len(args) < 2 {
		return fail(errors.New("want <seconds> <program> [argument...]"))
	}
	seconds, err := strconv.Atoi(args[0])
	if err != nil || seconds <= 0 {
		return fail(fmt.Errorf("bad number of seconds %q", args[0]))
	}
	// A group of its own, so that one signal reaches all descendants. Without
	// one that signal would reach the server, so nothing runs.
	err = syscall.Setpgid(0, 0)
	if syscall.Getpgrp() != os.Getpid() {
		return fail(fmt.Errorf("cannot make a process group: %v", err))
	}
	cmd := exec.Command(args[1], args[2:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return fail(err)
	}
	// Kills this process too.
	timer := time.AfterFunc(time.Duration(seconds)*time.Second, func() { _ = syscall.Kill(0, syscall.SIGKILL) })
	defer timer.Stop()
	err = cmd.Wait()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exit) && exit.ExitCode() >= 0:
		return exit.ExitCode()
	default:
		return fail(err)
	}
}
