package main

import (
	"fmt"
	"log"
	"os"
	"unicode"

	"golang.org/x/sys/unix"
)

func enableRawMode() (*unix.Termios, error) {
	raw, err := unix.IoctlGetTermios(0, unix.TCGETS)
	if err != nil {
		return nil, fmt.Errorf("failed to get termios: %v", err)
	}
	original := *raw
	raw.Lflag &^= unix.ECHO | unix.ICANON | unix.ISIG
	if err := unix.IoctlSetTermios(0, unix.TCSETS, raw); err != nil {
		return nil, fmt.Errorf("failed to set termios: %v", err)
	}
	return &original, nil
}

func restoreMode(state *unix.Termios) error {
	if err := unix.IoctlSetTermios(0, unix.TCSETS, state); err != nil {
		return fmt.Errorf("failed to restore termios: %v", err)
	}
	return nil
}

func main() {
	// raw mode
	state, err := enableRawMode()
	if err != nil {
		log.Fatal(err)
	}
	defer restoreMode(state)
	// byte reader loop
	b := make([]byte, 1)
	for {
		n, _ := os.Stdin.Read(b)
		if n != 1 || b[0] == 'q' {
			break
		}
		c := b[0]
		if unicode.IsPrint(rune(c)) {
			fmt.Printf("%d ('%c')\n", c, c)
		} else {
			fmt.Printf("%d\n", c)
		}
	}
}
