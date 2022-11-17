package main

import (
	"os"
)

func main() {
	b := make([]byte, 1)
	for {
		n, _ := os.Stdin.Read(b)
		if n != 1 || b[0] == 'q' {
			break
		}
	}
}
