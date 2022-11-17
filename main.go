package main

import (
	"fmt"
	"log"

	"golang.org/x/sys/unix"
)

var originalRaw *unix.Termios

func enableRawMode() {
	raw, err := unix.IoctlGetTermios(0, unix.TCGETS)
	if err != nil {
		log.Fatalf("failed to get termios: %v", err)
	}
	clone := *raw
	originalRaw = &clone
	raw.Iflag &^= unix.BRKINT | unix.ICRNL | unix.INPCK | unix.ISTRIP | unix.IXON
	raw.Oflag &^= unix.OPOST
	raw.Cflag &^= unix.CS8
	raw.Lflag &^= unix.ECHO | unix.ICANON | unix.IEXTEN | unix.ISIG
	raw.Cc[unix.VMIN] = 0
	raw.Cc[unix.VTIME] = 1
	if err := unix.IoctlSetTermios(unix.Stdin, unix.TCSETS, raw); err != nil {
		log.Fatalf("failed to set termios: %v", err)
	}
}

func restoreMode() {
	if err := unix.IoctlSetTermios(unix.Stdin, unix.TCSETS, originalRaw); err != nil {
		log.Fatalf("failed to restore termios: %v", err)
	}
}

func die(format string, args ...any) {
	editorRefreshScreen()
	msg := fmt.Sprintf(format, args...)
	unix.Write(unix.Stdout, []byte(msg))
	unix.Exit(0)
}

func controlKey(c byte) byte {
	return c & 0b00011111
}

func editorReadKey() byte {
	var b [1]byte
	for {
		n, err := unix.Read(unix.Stdin, b[:])
		if n == 1 {
			return b[0]
		}
		if n == -1 && err != unix.EAGAIN {
			die("read: %v", err)
		}
	}
}

func editorProcessKeypress() {
	c := editorReadKey()
	switch c {
	case controlKey('q'):
		editorRefreshScreen()
		restoreMode()
		unix.Exit(0)
	}
}

func editorRefreshScreen() {
	unix.Write(unix.Stdout, []byte("\x1b[2J"))
	unix.Write(unix.Stdout, []byte("\x1b[H"))
}

func main() {
	// raw mode
	enableRawMode()
	defer restoreMode()
	// byte reader loop
	for {
		editorRefreshScreen()
		editorProcessKeypress()
	}
}
