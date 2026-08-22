package main

// The Windows half of echo-free password entry. See password_posix.go for why
// this is done with the standard library rather than golang.org/x/term.
//
// syscall.NewLazyDLL is enough to reach kernel32 without golang.org/x/sys, so
// this costs no dependency.

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

// ENABLE_ECHO_INPUT from the Windows console API. Clearing this bit is what
// stops typed characters appearing.
const enableEchoInput uint32 = 0x0004

var (
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleMode = kernel32.NewProc("GetConsoleMode")
	procSetConsoleMode = kernel32.NewProc("SetConsoleMode")
)

func promptNoEcho(prompt string) (string, error) {
	handle := syscall.Handle(os.Stdin.Fd())

	original, err := consoleMode(handle)
	if err != nil {
		// Happens when stdin is a pipe or a file, or under a terminal emulator
		// that is not a real console. Refuse rather than prompt visibly.
		return "", fmt.Errorf("standard input is not a console, so echo cannot be disabled; set %s instead: %w",
			bootstrapPasswordEnv, err)
	}

	if err := setConsoleMode(handle, original&^enableEchoInput); err != nil {
		return "", fmt.Errorf("could not disable console echo, so refusing to prompt visibly; set %s instead: %w",
			bootstrapPasswordEnv, err)
	}
	defer func() { _ = setConsoleMode(handle, original) }()

	// The prompt goes to stderr so `adminctl ... > file` does not capture it.
	fmt.Fprint(os.Stderr, prompt)
	line, readErr := bufio.NewReader(os.Stdin).ReadString('\n')
	fmt.Fprintln(os.Stderr)

	password := strings.TrimRight(line, "\r\n")
	if readErr != nil && password == "" {
		return "", fmt.Errorf("reading the password: %w", readErr)
	}
	return password, nil
}

func consoleMode(handle syscall.Handle) (uint32, error) {
	var mode uint32
	ret, _, err := procGetConsoleMode.Call(uintptr(handle), uintptr(unsafe.Pointer(&mode)))
	if ret == 0 {
		return 0, err
	}
	return mode, nil
}

func setConsoleMode(handle syscall.Handle, mode uint32) error {
	ret, _, err := procSetConsoleMode.Call(uintptr(handle), uintptr(mode))
	if ret == 0 {
		return err
	}
	return nil
}
