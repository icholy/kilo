package main

import (
	"fmt"
	"log"
	"os"

	"golang.org/x/sys/unix"
)

func enableRawMode() error {
	raw, err := unix.IoctlGetTermios(0, unix.TCGETS)
	if err != nil {
		return fmt.Errorf("failed to get termios: %v", err)
	}
	raw.Lflag &^= unix.ECHO
	if err := unix.IoctlSetTermios(0, unix.TCSETS, raw); err != nil {
		return fmt.Errorf("failed to set termios: %v", err)
	}
	return nil
}

func main() {
	if err := enableRawMode(); err != nil {
		log.Fatal(err)
	}
	// byte reader loop
	b := make([]byte, 1)
	for {
		n, _ := os.Stdin.Read(b)
		if n != 1 || b[0] == 'q' {
			break
		}
	}
}
