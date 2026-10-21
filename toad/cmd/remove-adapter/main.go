//go:build windows

package main

import (
	"encoding/binary"
	"fmt"
	"os"

	"github.com/google/uuid"
	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wintun"
)

func tunnelGUID(name string) windows.GUID {
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte("kikimora-toad:"+name))
	var guid windows.GUID
	guid.Data1 = binary.BigEndian.Uint32(id[0:4])
	guid.Data2 = binary.BigEndian.Uint16(id[4:6])
	guid.Data3 = binary.BigEndian.Uint16(id[6:8])
	copy(guid.Data4[:], id[8:16])
	return guid
}

func main() {
	name := "kk-awg0"
	if len(os.Args) > 1 {
		name = os.Args[1]
	}
	guid := tunnelGUID(name)
	fmt.Printf("GUID for %s: %08x-%04x-%04x-%x-%x\n", name, guid.Data1, guid.Data2, guid.Data3, guid.Data4[:2], guid.Data4[2:])

	// Try to open and close
	adapter, err := wintun.OpenAdapter(name)
	if err != nil {
		fmt.Printf("OpenAdapter(%s) failed: %v\n", name, err)
		fmt.Println("Attempting CreateAdapter to take ownership...")
		a, err2 := wintun.CreateAdapter(name, "Kikimora Toad", &guid)
		if err2 != nil {
			fmt.Printf("CreateAdapter also failed: %v\n", err2)
			return
		}
		fmt.Println("CreateAdapter succeeded, closing...")
		a.Close()
		fmt.Println("Adapter removed via CreateAdapter+Close")
		return
	}
	fmt.Printf("OpenAdapter succeeded, closing...\n")
	adapter.Close()
	fmt.Println("Adapter removed")
}
