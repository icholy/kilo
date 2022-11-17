package main

import (
	"fmt"
	"log"
	"unicode"

	"golang.org/x/sys/unix"
)

func enableRawMode() (*unix.Termios, error) {
	raw, err := unix.IoctlGetTermios(0, unix.TCGETS)
	if err != nil {
		return nil, fmt.Errorf("failed to get termios: %v", err)
	}
	original := *raw
	raw.Iflag &^= unix.BRKINT | unix.ICRNL | unix.INPCK | unix.ISTRIP | unix.IXON
	raw.Oflag &^= unix.OPOST
	raw.Cflag &^= unix.CS8
	raw.Lflag &^= unix.ECHO | unix.ICANON | unix.IEXTEN | unix.ISIG
	raw.Cc[unix.VMIN] = 0
	raw.Cc[unix.VTIME] = 1
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

func controlKey(c byte) byte {
	return c & 0b00011111
}

func main() {
	// raw mode
	state, err := enableRawMode()
	if err != nil {
		log.Fatal(err)
	}
	defer restoreMode(state)
	// byte reader loop
	b := []byte{0}
	for {
		if _, err := unix.Read(0, b); err != nil && err != unix.EAGAIN {
			log.Fatalf("failed to read: %v", err)
		}
		c := b[0]
		if unicode.IsPrint(rune(c)) {
			fmt.Printf("%d ('%c')\r\n", c, c)
		} else {
			fmt.Printf("%d\r\n", c)
		}
		if c == controlKey('q') {
			break
		}
	}
}
