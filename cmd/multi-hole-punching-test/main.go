package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/pion/stun"
	"github.com/pion/transport/v2"
	"github.com/pion/transport/v2/stdnet"
)

type ReusedConn struct {
	*net.UDPConn
	remoteAddr net.Addr
}

func (rc *ReusedConn) Write(b []byte) (int, error) {
	return rc.UDPConn.WriteTo(b, rc.remoteAddr)
}

type CustomNet struct {
	*stdnet.Net
	conn *net.UDPConn
}

func (cn *CustomNet) Dial(network string, address string) (net.Conn, error) {
	dst, err := net.ResolveUDPAddr("udp4", address)
	if err != nil {
		return nil, err
	}
	return &ReusedConn{
		UDPConn:    cn.conn,
		remoteAddr: dst,
	}, nil
}

func getPublicAddr(conn *net.UDPConn) (string, int, error) {
	u, err := stun.ParseURI("stun:stun.l.google.com:19302")
	if err != nil {
		return "", 0, err
	}

	nw, err := stdnet.NewNet()
	if err != nil {
		return "", 0, fmt.Errorf("failed to create net: %w", err)
	}

	var net transport.Net = nil

	if conn != nil {
		net = &CustomNet{
			Net:  nw,
			conn: conn,
		}
	}

	// Creating a "connection" to STUN server.
	c, err := stun.DialURI(u, &stun.DialConfig{
		Net: net,
	})
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

var gotFirstResponse atomic.Bool

type PortInfo struct {
	PeerPort  int
	LocalPort int
}

func waitForResponse(conn *net.UDPConn, done chan PortInfo) {
	go func() {
		for {
			buf := make([]byte, 1024)
			conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			n, peerAddr, err := conn.ReadFromUDP(buf)
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					break
				}
				if !errors.Is(err, os.ErrDeadlineExceeded) {
					fmt.Fprintf(os.Stderr, "error: %s\n", err)
				}
				continue
			}
			// host, port, err := net.SplitHostPort(addr.String())
			// if err != nil {
			// 	fmt.Fprintf(os.Stderr, "error: %s\n", err)
			// 	return
			// }
			// fmt.Printf("got a response from %s:%s with message %s\n", host, port, buf[0:n])
			log.Printf("%s sent a response: %s\n", peerAddr.String(), buf[0:n])
			if gotFirstResponse.CompareAndSwap(false, true) {
				_, localPortStr, err := net.SplitHostPort(conn.LocalAddr().String())
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: %s\n", err)
					return
				}
				localPort, err := strconv.Atoi(localPortStr)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: %s\n", err)
					return
				}

				done <- PortInfo{
					PeerPort:  peerAddr.Port,
					LocalPort: localPort,
				}
			}
			// break
		}
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

	pubIP, pubPort, err := getPublicAddr(conn)
	if err != nil {
		return err
	}

	fmt.Printf("%s -> %s:%d\n", conn.LocalAddr().String(), pubIP, pubPort)
	fmt.Println("Press Enter to continue")
	fmt.Scanln()

	done := make(chan PortInfo, 1)
	waitForResponse(conn, done)

	var portInfo PortInfo
	sleepDuration := 10 * time.Millisecond
	var remoteAddr string

	cnt := 5

	for cnt > 0 {
		if !gotFirstResponse.Load() {
			remoteAddr = fmt.Sprintf("%s:%d", remoteIP, 1024+rand.Intn(65536-1024))
			fmt.Printf("trying %s ...\n", remoteAddr)
		} else {
			cnt--
		}

		dst, err := net.ResolveUDPAddr("udp4", remoteAddr)
		if err != nil {
			return err
		}

		for i := 0; i < 5; i++ {
			_, err = conn.WriteTo([]byte(fmt.Sprintf("Hello from %s:%d!", pubIP, pubPort)), dst)
			if err != nil {
				return err
			}
		}

		select {
		case portInfo = <-done:
			remoteAddr = fmt.Sprintf("%s:%d", remoteIP, portInfo.PeerPort)
			sleepDuration = 50 * time.Millisecond
		default:
		}

		time.Sleep(sleepDuration)
	}

	fmt.Printf("Remote addr: %s\n", remoteAddr)
	return nil
}

func guessLocalPort(remoteAddr string) error {
	dst, err := net.ResolveUDPAddr("udp4", remoteAddr)
	if err != nil {
		return err
	}

	const portCount = 384
	var conns [portCount]*net.UDPConn

	for i := 0; i < portCount; {
		localAddr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf(":%d", 1024+rand.Intn(65536-1024)))
		if err != nil {
			return err
		}
		conns[i], err = net.ListenUDP("udp4", localAddr)
		if err != nil {
			continue
		}
		i++
	}

	pubIP, _, err := getPublicAddr(nil)
	if err != nil {
		return err
	}

	allDone := make(chan bool)
	var portInfo PortInfo

	for i := 0; i < portCount; i++ {
		idx := i

		go func() {
			conn := conns[idx]
			fmt.Printf("trying %s ...\n", conn.LocalAddr().String())

			done := make(chan PortInfo, 1)
			waitForResponse(conn, done)

		loop:
			for {
				for i := 0; i < 5; i++ {
					_, err = conn.WriteTo([]byte(fmt.Sprintf("Hello from %s!", pubIP)), dst)
					if err != nil {
						fmt.Fprintf(os.Stderr, "error: %s\n", err)
						return
					}
				}

				select {
				case portInfo = <-done:
					allDone <- true
					break loop
				default:
				}

				time.Sleep(200 * time.Millisecond)
			}

		}()
	}
	<-allDone

	var conn *net.UDPConn
	for _, c := range conns {
		if _, port, err := net.SplitHostPort(c.LocalAddr().String()); err == nil && port == strconv.Itoa(portInfo.LocalPort) {
			conn = c
			continue
		}
		c.Close()
	}
	if conn == nil {
		log.Fatal("Conn is nil")
	}

	// send couple more packets so there's a higher chance
	// the peer will receive at least one of them (TODO: proper ack)
	for j := 0; j < 5; j++ {
		for i := 0; i < 5; i++ {
			_, err = conn.WriteTo([]byte(fmt.Sprintf("Hello from %s!", pubIP)), dst)
			if err != nil {
				return err
			}
		}

		time.Sleep(50 * time.Millisecond)
	}

	fmt.Printf("Local addr: :%d", portInfo.LocalPort)
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

	pubIP, pubPort, err := getPublicAddr(conn)
	if err != nil {
		return err
	}

	fmt.Printf("%s -> %s:%d\n", conn.LocalAddr().String(), pubIP, pubPort)
	fmt.Println("Enter remote port:")
	var remotePort int
	fmt.Scanln(&remotePort)

	fmt.Printf("Sending packets to %s:%d ...\n", remoteIP, remotePort)

	done := make(chan PortInfo, 1)
	waitForResponse(conn, done)

	remoteAddr := fmt.Sprintf("%s:%d", remoteIP, remotePort)
	fmt.Printf("trying %s ...\n", remoteAddr)
	dst, err := net.ResolveUDPAddr("udp4", remoteAddr)
	if err != nil {
		return err
	}

	// loop:
	for {
		for i := 0; i < 5; i++ {
			_, err = conn.WriteTo([]byte(fmt.Sprintf("Hello from %s:%d!", pubIP, pubPort)), dst)
			if err != nil {
				return err
			}
		}

		select {
		case <-done:
			// break loop
		default:
		}

		time.Sleep(50 * time.Millisecond)
	}

	// return nil
}
