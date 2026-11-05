//go:build !kikimora_test_failpoints

package control

import "net/netip"

// processSelectedFailpoint is a no-op in release builds: no environment
// variable is read and the process never exits on a durable boundary
// (08D1 §22.2). Release/package builds never use the test tag.
func processSelectedFailpoint(string, string, netip.Prefix) {}
