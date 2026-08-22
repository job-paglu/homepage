//go:build !windows

package main

// Reading a password without echoing it normally means golang.org/x/term. This
// module is capped at two direct dependencies (pgx and x/crypto), so echo
// control is done with the standard library instead: `stty` on POSIX, the
// console-mode API on Windows.
//
// The trade-off is honest: shelling out to stty is less elegant than an ioctl,
// but it is portable across every POSIX system that has a shell, needs no cgo,
// and — critically — fails loudly. If echo cannot be turned off the prompt is
// refused rather than shown, so a password is never typed onto a visible screen.

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func promptNoEcho(prompt string) (string, error) {
	// /dev/tty rather than stdin, so the prompt still works when stdin is a pipe
	// — which is exactly the case where a careless implementation would read the
	// password from a script and echo it.
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return "", fmt.Errorf("no terminal is available to prompt on; set %s instead", bootstrapPasswordEnv)
	}
	defer func() { _ = tty.Close() }()

	if err := setEcho(tty, false); err != nil {
		return "", fmt.Errorf("could not disable terminal echo, so refusing to prompt visibly; set %s instead: %w",
			bootstrapPasswordEnv, err)
	}
	// Restoring echo matters even on the error paths: leaving a terminal with
	// echo off is a genuinely unpleasant thing to do to someone.
	defer func() { _ = setEcho(tty, true) }()

	fmt.Fprint(tty, prompt)
	line, readErr := bufio.NewReader(tty).ReadString('\n')
	// The user's Enter was not echoed, so the cursor is still on the prompt line.
	fmt.Fprintln(tty)

	password := strings.TrimRight(line, "\r\n")
	if readErr != nil && password == "" {
		return "", fmt.Errorf("reading the password: %w", readErr)
	}
	return password, nil
}

func setEcho(tty *os.File, enabled bool) error {
	flag := "-echo"
	if enabled {
		flag = "echo"
	}

	cmd := exec.Command("stty", flag)
	// stty acts on its own stdin, so the terminal has to be wired to it.
	cmd.Stdin = tty
	cmd.Stdout = tty
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
