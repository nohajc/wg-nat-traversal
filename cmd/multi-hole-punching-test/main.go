package main

import (
	"flag"
	"fmt"
	"math/rand"
	"net"
	"os"
	"time"

	"github.com/pion/stun"
)

func getPublicAddr() (string, int, error) {
	u, err := stun.ParseURI("stun:stun.l.google.com:19302")
	if err != nil {
		return "", 0, err
	}

	// Creating a "connection" to STUN server.
	c, err := stun.DialURI(u, &stun.DialConfig{})
	if err != nil {
		return "", 0, err
	}
	// Building binding request with random transaction id.
	message := stun.MustBuild(stun.TransactionID, stun.BindingRequest)

	var IP string
	var port int
	var cbErr error
	// Sending request to STUN server, waiting for response message.
	if err := c.Do(message, func(res stun.Event) {
		if res.Error != nil {
			panic(res.Error)
		}
		// Decoding XOR-MAPPED-ADDRESS attribute from message.
		var xorAddr stun.XORMappedAddress
		if err := xorAddr.GetFrom(res.Message); err != nil {
			cbErr = err
			return
		}
		IP = xorAddr.IP.String()
		port = xorAddr.Port
	}); err != nil {
		return "", 0, err
	}
	if cbErr != nil {
		return "", 0, err
	}
	return IP, port, nil
}

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
		err = guessRemotePort(remoteAddr)
	} else if natType == "hard" {
		err = guessLocalPort(remoteAddr)
	} else {
		// fmt.Fprintln(os.Stderr, "error: invalid NAT type; specify easy or hard")
		// os.Exit(1)
		err = simpleTest(remoteAddr)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
	}
}

func waitForResponse(conn net.PacketConn, done chan bool) {
	go func() {
		buf := make([]byte, 65536)
		n, addr, err := conn.ReadFrom(buf)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %s\n", err)
			return
		}
		host, port, err := net.SplitHostPort(addr.String())
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %s\n", err)
			return
		}
		fmt.Printf("got a response from %s:%s with message %s\n", host, port, buf[0:n])
		done <- true
	}()
}

func guessRemotePort(remoteIP string) error {
	localAddr, err := net.ResolveUDPAddr("udp4", ":0")
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp4", localAddr)
	if err != nil {
		return err
	}

	pubIP, pubPort, err := getPublicAddr()
	if err != nil {
		return err
	}

	fmt.Printf("%s -> %s:%d\n", conn.LocalAddr().String(), pubIP, pubPort)
	fmt.Println("Press Enter to continue")
	fmt.Scanln()

	done := make(chan bool)
	waitForResponse(conn, done)

loop:
	for {
		remoteAddr := fmt.Sprintf("%s:%d", remoteIP, rand.Intn(65536))
		fmt.Printf("trying %s ...\n", remoteAddr)
		dst, err := net.ResolveUDPAddr("udp4", remoteAddr)
		if err != nil {
			return err
		}

		for i := 0; i < 10; i++ {
			_, err = conn.WriteTo([]byte(remoteAddr), dst)
			if err != nil {
				return err
			}
			time.Sleep(1 * time.Millisecond)
		}

		select {
		case <-done:
			break loop
		default:
		}

		// time.Sleep(10 * time.Millisecond)
	}

	return nil
}

func guessLocalPort(remoteAddr string) error {
	dst, err := net.ResolveUDPAddr("udp4", remoteAddr)
	if err != nil {
		return err
	}

	const portCount = 384
	var conns [portCount]net.PacketConn

	for i := 0; i < portCount; {
		conns[i], err = net.ListenPacket("udp4", fmt.Sprintf(":%d", 1024+rand.Intn(65536-1024)))
		if err != nil {
			continue
		}
		i++
	}

	allDone := make(chan bool)
	for i := 0; i < portCount; i++ {
		idx := i
		go func() {
			conn := conns[idx]
			fmt.Printf("trying %s ...\n", conn.LocalAddr().String())

			done := make(chan bool)
			waitForResponse(conn, done)

			for {
				// for i := 0; i < 5; i++ {
				_, err = conn.WriteTo([]byte(remoteAddr), dst)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: %s\n", err)
					return
				}
				// }

				select {
				case <-done:
					allDone <- true
				default:
				}

				time.Sleep(2 * time.Millisecond)
			}
		}()
	}
	<-allDone

	return nil
}

func simpleTest(remoteIP string) error {
	localAddr, err := net.ResolveUDPAddr("udp4", ":0")
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp4", localAddr)
	if err != nil {
		return err
	}

	pubIP, pubPort, err := getPublicAddr()
	if err != nil {
		return err
	}

	fmt.Printf("%s -> %s:%d\n", conn.LocalAddr().String(), pubIP, pubPort)
	fmt.Println("Enter remote port:")
	var remotePort int
	fmt.Scanln(&remotePort)

	fmt.Printf("Sending packets to %s:%d ...\n", remoteIP, remotePort)

	done := make(chan bool)
	waitForResponse(conn, done)

	remoteAddr := fmt.Sprintf("%s:%d", remoteIP, remotePort)
	fmt.Printf("trying %s ...\n", remoteAddr)
	dst, err := net.ResolveUDPAddr("udp4", remoteAddr)
	if err != nil {
		return err
	}

loop:
	for {
		_, err = conn.WriteTo([]byte(remoteAddr), dst)
		if err != nil {
			return err
		}

		select {
		case <-done:
			break loop
		default:
		}

		time.Sleep(5 * time.Second)
	}

	return nil
}
