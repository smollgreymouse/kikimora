package main

import (
	"fmt"

	"github.com/Microsoft/go-winio"
	_ "github.com/smollgreymouse/kikimora/toad/internal/config"
	_ "github.com/smollgreymouse/kikimora/toad/internal/control"
	_ "github.com/smollgreymouse/kikimora/toad/internal/endpoint"
	_ "github.com/smollgreymouse/kikimora/toad/internal/leshy"
	_ "github.com/smollgreymouse/kikimora/toad/internal/platform"
	_ "github.com/smollgreymouse/kikimora/toad/internal/supervisor"
)

func main() {
	l, err := winio.ListenPipe(`\.\pipe\kikimora-zzprobe-x`, &winio.PipeConfig{SecurityDescriptor: "D:P(A;;GA;;;SY)(A;;GA;;;BA)"})
	if err != nil {
		fmt.Println("FAIL:", err)
		return
	}
	l.Close()
	fmt.Println("OK")
}
