package main

import (
	"fmt"
	"log"
	"os"
	"unicode"

	"golang.org/x/term"
)

func main() {
	// enter raw mode
	state, err := term.MakeRaw(0)
	if err != nil {
		log.Fatal(err)
	}
	// restore state
	defer term.Restore(0, state)
	// byte reader loop
	b := make([]byte, 1)
	for {
		n, _ := os.Stdin.Read(b)
		if n != 1 || b[0] == 'q' {
			break
		}
		c := b[0]
		if unicode.IsPrint(rune(c)) {
			fmt.Printf("%d ('%c')\n\r", c, c)
		} else {
			fmt.Printf("%d\n\r", c)
		}
	}
}
