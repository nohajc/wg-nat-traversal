package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/nohajc/wg-nat-traversal/common/nat"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: multi-hole-punching-test <options> REMOTE_IP[:PORT]\n")
		flag.PrintDefaults()
	}

	var natType string
	flag.StringVar(&natType, "src-nat-type", "", "easy|hard (type of NAT on the client side)")
	flag.Parse()

	if flag.NArg() != 1 {
		fmt.Fprintf(os.Stderr, "error: missing remote IP")
		os.Exit(1)
	}
	remoteAddr := flag.Arg(0)

	var err error
	if natType == "easy" {
		_, err = nat.GuessRemotePort(remoteAddr, nat.Interactive(true))
	} else if natType == "hard" {
		_, err = nat.GuessLocalPort(remoteAddr)
	} else {
		// fmt.Fprintln(os.Stderr, "error: invalid NAT type; specify easy or hard")
		// os.Exit(1)
		err = nat.SimpleTest(remoteAddr)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
	}
}
