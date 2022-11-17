package main

import (
	"fmt"
	"io"
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
		_, err := os.Stdin.Read(b)
		if err != nil && err != io.EOF {
			log.Print(err)
		}
		c := b[0]
		if unicode.IsPrint(rune(c)) {
			fmt.Printf("%d ('%c')\r\n", c, c)
		} else {
			fmt.Printf("%d\r\n", c)
		}
		if c == 'q' {
			break
		}
	}
}
