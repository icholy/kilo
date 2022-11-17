package main

import (
	"errors"
	"fmt"
	"log"

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

func editorReadKey() byte {
	var b [1]byte
	for {
		n, err := unix.Read(0, b[:])
		if n == 1 {
			return b[0]
		}
		if n == -1 && err != unix.EAGAIN {
			log.Fatalf("failed to read byte: %v", err)
		}
	}
}

var ErrExit = errors.New("exit")

func editorProcessKeypress() error {
	c := editorReadKey()
	switch c {
	case controlKey('q'):
		return ErrExit
	}
	return nil
}

func editorRefreshScreen() error {
	_, err := unix.Write(1, []byte("\x1b[2J"))
	return err
}

func main() {
	// raw mode
	state, err := enableRawMode()
	if err != nil {
		log.Fatal(err)
	}
	defer restoreMode(state)
	// byte reader loop
	for {
		if err := editorRefreshScreen(); err != nil {
			log.Fatalf("refresh screen: %v", err)
		}
		err := editorProcessKeypress()
		if err == ErrExit {
			break
		}
		if err != nil {
			log.Fatalf("process keypress: %v", err)
		}
	}
}
