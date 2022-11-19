package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

const version = "0.0.1"

type Row struct {
	chars []byte
}

var E struct {
	termios    unix.Termios
	screenrows int
	screencols int
	cx         int
	cy         int
	numrows    int
	rowoff     int
	rows       []Row
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

func editorOpen(filename string) {
	f, err := os.Open(filename)
	if err != nil {
		die("failed to open file: %s", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		E.rows = append(E.rows, Row{
			chars: sc.Bytes(),
		})
		E.numrows++
	}
	if err := sc.Err(); err != nil {
		die("failed to read file: %s", err)
	}
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

func controlKey(c byte) int {
	return int(c & 0b00011111)
}

const (
	ArrowLeft = iota + 1000
	ArrowRight
	ArrowUp
	ArrowDown
	PageUp
	PageDown
	HomeKey
	EndKey
	DeleteKey
)

func editorReadKey() int {
	var c int
	var b [1]byte
	for {
		n, err := unix.Read(unix.Stdin, b[:])
		if n == 1 {
			c = int(b[0])
			break
		}
		if n == -1 && err != unix.EAGAIN {
			die("read: %v", err)
		}
	}
	// handle escale sequences
	if c == '\x1b' {
		var seq [3]byte
		if n, _ := unix.Read(unix.Stdin, seq[:1]); n != 1 {
			return c
		}
		if n, _ := unix.Read(unix.Stdin, seq[1:2]); n != 1 {
			return c
		}
		if seq[0] == '[' {
			// page up/page down
			if seq[1] >= '0' && seq[1] <= '9' {
				if n, _ := unix.Read(unix.Stdin, seq[2:]); n != 1 {
					return c
				}
				if seq[2] == '~' {
					switch seq[1] {
					case '3':
						return DeleteKey
					case '5':
						return PageUp
					case '6':
						return PageDown
					case '1', '7':
						return HomeKey
					case '4', '8':
						return EndKey
					}
				}
			}
			// arrow keys
			switch seq[1] {
			case 'A':
				return ArrowUp
			case 'B':
				return ArrowDown
			case 'C':
				return ArrowRight
			case 'D':
				return ArrowLeft
			case 'H':
				return HomeKey
			case 'F':
				return EndKey
			}
		} else {
			if seq[0] == 'O' {
				switch seq[1] {
				case 'H':
					return HomeKey
				case 'F':
					return EndKey
				}
			}
		}
	}
	return c
}

func editorProcessKeypress() {
	c := editorReadKey()
	switch c {
	case controlKey('q'):
		editorRefreshScreen()
		restoreMode()
		unix.Exit(0)
	case ArrowUp, ArrowDown, ArrowLeft, ArrowRight:
		editorMoveCursor(c)
	case PageUp:
		for i := 0; i < E.screenrows; i++ {
			editorMoveCursor(ArrowUp)
		}
	case PageDown:
		for i := 0; i < E.screenrows; i++ {
			editorMoveCursor(ArrowDown)
		}
	case HomeKey:
		E.cx = 0
	case EndKey:
		E.cx = E.screencols
	case DeleteKey:
		editorMoveCursor(ArrowLeft)
	}
}

func editorMoveCursor(c int) {
	switch c {
	case ArrowUp:
		if E.cy > 0 {
			E.cy--
		}
	case ArrowDown:
		if E.cy < E.numrows {
			E.cy++
		}
	case ArrowLeft:
		if E.cx > 0 {
			E.cx--
		}
	case ArrowRight:
		if E.cx < E.screencols {
			E.cx++
		}
	}
}

func editorScroll() {
	if E.cy < E.rowoff {
		E.rowoff = E.cy
	}
	if E.cy >= E.rowoff+E.screenrows {
		E.rowoff = E.cy - E.screenrows + 1
	}
}

func editorRefreshScreen() {
	editorScroll()
	var b bytes.Buffer
	b.WriteString("\x1b[?25l") // hide cursor
	b.WriteString("\x1b[H")    // put cursor at top left
	editorDrawRows(&b)
	fmt.Fprintf(&b, "\x1b[%d;%dH", E.cy-E.rowoff+1, E.cx+1) // move cursor to correct position
	b.WriteString("\x1b[?25h")                              // show cursor
	unix.Write(unix.Stdout, b.Bytes())
}

func editorDrawRows(b *bytes.Buffer) {
	for y := 0; y < E.screenrows; y++ {
		filerow := y + E.rowoff
		if filerow >= E.numrows {
			// print welcome screen
			if E.numrows == 0 && y == E.screenrows/3 {
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
		} else {
			line := E.rows[filerow].chars
			if len(line) > E.screencols {
				line = line[:E.screencols]
			}
			b.Write(line)
		}
		b.WriteString("\x1b[K") // clear one line
		if y < E.screenrows-1 {
			b.WriteString("\r\n")
		}
	}
}

func main() {
	flag.Parse()
	// raw mode
	enableRawMode()
	defer restoreMode()
	// setup
	initEditor()
	if flag.NArg() > 0 {
		editorOpen(flag.Arg(0))
	}
	// byte reader loop
	for {
		editorRefreshScreen()
		editorProcessKeypress()
	}
}
