package main

import (
	"bytes"
	"fmt"
	"log"
	"strings"

	"golang.org/x/sys/unix"
)

const version = "0.0.1"

var E struct {
	termios    unix.Termios
	screenrows int
	screencols int
	cx         int
	cy         int
}

func enableRawMode() {
	raw, err := unix.IoctlGetTermios(0, unix.TCGETS)
	if err != nil {
		log.Fatalf("failed to get termios: %v", err)
	}
	E.termios = *raw
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
	if err := unix.IoctlSetTermios(unix.Stdin, unix.TCSETS, &E.termios); err != nil {
		log.Fatalf("failed to restore termios: %v", err)
	}
}

func die(format string, args ...any) {
	editorRefreshScreen()
	msg := fmt.Sprintf(format, args...)
	unix.Write(unix.Stdout, []byte(msg))
	unix.Exit(0)
}

func initEditor() {
	E.screenrows, E.screencols = getWindowSize()
}

func getWindowSize() (rows, cols int) {
	ws, err := unix.IoctlGetWinsize(unix.Stdout, unix.TIOCGWINSZ)
	if err != nil {
		// fallback mechanism
		if _, err := unix.Write(unix.Stdout, []byte("\x1b[999C\x1b[999B")); err != nil {
			die("failed to get window size: %v", err)
		}
		return getCursorPosition()

	}
	return int(ws.Row), int(ws.Col)
}

func getCursorPosition() (row, col int) {
	if _, err := unix.Write(unix.Stdout, []byte("\x1b[6n")); err != nil {
		die("getCursorPosition: %v", err)
	}
	var buf [32]byte
	var i int
	for i < len(buf)-1 {
		if n, _ := unix.Read(unix.Stdin, buf[i:i+1]); n != 1 {
			break
		}
		if buf[i] == 'R' {
			break
		}
		i++
	}
	if buf[0] != '\x1b' || buf[1] != '[' {
		die("invalid escape sequence")
	}
	if n, err := fmt.Sscanf(string(buf[2:i]), "%d;%d", &row, &col); n != 2 {
		die("failed to scan cursor pos: %v", err)
	}
	return row, col
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
	case 'w':
		E.cy--
	case 's':
		E.cy++
	case 'a':
		E.cx--
	case 'd':
		E.cx++
	}
}

func editorRefreshScreen() {
	var b bytes.Buffer
	b.WriteString("\x1b[?25l") // hide cursor
	b.WriteString("\x1b[H")    // put cursor at top left
	editorDrawRows(&b)
	fmt.Fprintf(&b, "\x1b[%d;%dH", E.cy+1, E.cx+1) // move cursor to correct position
	b.WriteString("\x1b[?25h")                     // show cursor
	unix.Write(unix.Stdout, b.Bytes())
}

func editorDrawRows(b *bytes.Buffer) {
	for y := 0; y < E.screenrows; y++ {
		// print welcome screen
		if y == E.screenrows/3 {
			welcome := fmt.Sprintf("Kilo editor -- version %s", version)
			if len(welcome) > E.screencols {
				welcome = welcome[:E.screencols]
			}
			padding := (E.screencols - len(welcome)) / 2
			b.WriteString(strings.Repeat(" ", padding))
			b.WriteString(welcome)
		} else {
			b.WriteString("~")
		}
		b.WriteString("\x1b[K") // clear one line
		if y < E.screenrows-1 {
			b.WriteString("\r\n")
		}
	}
}

func main() {
	// raw mode
	enableRawMode()
	initEditor()
	defer restoreMode()
	// byte reader loop
	for {
		editorRefreshScreen()
		editorProcessKeypress()
	}
}
