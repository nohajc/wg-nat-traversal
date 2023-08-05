package utils

import (
	"errors"
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

type STUNSrv string

const STUN_Google STUNSrv = "stun:stun.l.google.com:19302"
const STUN_VoipGATE STUNSrv = "stun.voipgate.com:3478"

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
	dst, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		return nil, err
	}
	return &ReusedConn{
		UDPConn:    cn.conn,
		remoteAddr: dst,
	}, nil
}

func GetPublicAddr(conn *net.UDPConn) (string, int, error) {
	return STUN_Google.getPublicAddr(conn)
}

func (s STUNSrv) getPublicAddr(conn *net.UDPConn) (string, int, error) {
	u, err := stun.ParseURI(string(s))
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
	client, err := stun.DialURI(u, &stun.DialConfig{
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
	if err := client.Do(message, func(res stun.Event) {
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

var gotFirstResponse atomic.Bool

type PortInfo struct {
	PeerPort  int
	LocalPort int
}

func waitForResponse(conn *net.UDPConn, resolved chan PortInfo, acked chan bool) {
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

				resolved <- PortInfo{
					PeerPort:  peerAddr.Port,
					LocalPort: localPort,
				}
			}
			if string(buf[0:n]) == "RESOLVED" {
				acked <- true
			}
			// break
		}
	}()
}

func GuessRemotePort(remoteIP string) (int, error) {
	localAddr, err := net.ResolveUDPAddr("udp", ":0")
	if err != nil {
		return 0, err
	}
	conn, err := net.ListenUDP("udp", localAddr)
	if err != nil {
		return 0, err
	}
	defer conn.Close()

	pubIP, pubPort, err := GetPublicAddr(conn)
	if err != nil {
		return 0, err
	}

	fmt.Printf("%s -> %s:%d\n", conn.LocalAddr().String(), pubIP, pubPort)
	fmt.Println("Press Enter to continue")
	fmt.Scanln()

	resolved := make(chan PortInfo, 1)
	acked := make(chan bool, 1)
	waitForResponse(conn, resolved, acked)

	var portInfo PortInfo
	sleepDuration := 5 * time.Millisecond
	var remoteAddr string

	message := "UNKNOWN"
	cnt := 10
	wasAcked := false

	for cnt > 0 {
		if !gotFirstResponse.Load() {
			remoteAddr = fmt.Sprintf("%s:%d", remoteIP, 1024+rand.Intn(65536-1024))
			fmt.Printf("trying %s ...\n", remoteAddr)
		} else if wasAcked {
			cnt--
		}

		dst, err := net.ResolveUDPAddr("udp", remoteAddr)
		if err != nil {
			return 0, err
		}

		for i := 0; i < 10; i++ {
			_, err = conn.WriteTo([]byte(message), dst)
			if err != nil {
				return 0, err
			}
		}

		select {
		case portInfo = <-resolved:
			remoteAddr = fmt.Sprintf("%s:%d", remoteIP, portInfo.PeerPort)
			sleepDuration = 50 * time.Millisecond
			message = "RESOLVED"

			fmt.Printf("Remote addr: %s\n", remoteAddr)
		default:
		}

		// make sure resolved was received
		// before we try to receive acked
		if message == "RESOLVED" {
			select {
			case <-acked:
				wasAcked = true
			default:
			}
		}

		time.Sleep(sleepDuration)
	}

	fmt.Printf("Remote addr: %s\n", remoteAddr)
	return portInfo.PeerPort, nil
}

func GuessLocalPort(remoteAddr string) (int, error) {
	dst, err := net.ResolveUDPAddr("udp", remoteAddr)
	if err != nil {
		return 0, err
	}

	const portCount = 384
	var conns [portCount]*net.UDPConn

	for i := 0; i < portCount; {
		localAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf(":%d", 1024+rand.Intn(65536-1024)))
		if err != nil {
			return 0, err
		}
		conns[i], err = net.ListenUDP("udp", localAddr)
		if err != nil {
			continue
		}
		i++
	}

	// pubIP, _, err := GetPublicAddr(nil)
	// if err != nil {
	// 	return err
	// }

	allDone := make(chan bool, 1)
	acked := make(chan bool, 1)

	var portInfo PortInfo

	for i := 0; i < portCount; i++ {
		idx := i

		go func() {
			conn := conns[idx]
			fmt.Printf("trying %s ...\n", conn.LocalAddr().String())

			resolved := make(chan PortInfo, 1)
			waitForResponse(conn, resolved, acked)

		loop:
			for {
				for i := 0; i < 5; i++ {
					_, err = conn.WriteTo([]byte("UNKNOWN"), dst)
					if err != nil {
						if !errors.Is(err, net.ErrClosed) {
							fmt.Fprintf(os.Stderr, "error: %s\n", err)
						}
						return
					}
				}

				select {
				case portInfo = <-resolved:
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

	fmt.Printf("Local addr: :%d\n", portInfo.LocalPort)

loop:
	for {
		for i := 0; i < 5; i++ {
			_, err = conn.WriteTo([]byte("RESOLVED"), dst)
			if err != nil {
				return 0, err
			}
		}

		select {
		case <-acked:
			break loop
		default:
		}
		time.Sleep(50 * time.Millisecond)
	}

	fmt.Printf("Local addr: :%d\n", portInfo.LocalPort)
	conn.Close()
	return portInfo.LocalPort, nil
}

func SimpleTest(remoteIP string) error {
	localAddr, err := net.ResolveUDPAddr("udp", ":0")
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp", localAddr)
	if err != nil {
		return err
	}

	pubIP, pubPort, err := GetPublicAddr(conn)
	if err != nil {
		return err
	}

	fmt.Printf("%s -> %s:%d\n", conn.LocalAddr().String(), pubIP, pubPort)
	fmt.Println("Enter remote port:")
	var remotePort int
	fmt.Scanln(&remotePort)

	fmt.Printf("Sending packets to %s:%d ...\n", remoteIP, remotePort)

	done := make(chan PortInfo, 1)
	acked := make(chan bool, 1)
	waitForResponse(conn, done, acked)

	remoteAddr := fmt.Sprintf("%s:%d", remoteIP, remotePort)
	fmt.Printf("trying %s ...\n", remoteAddr)
	dst, err := net.ResolveUDPAddr("udp", remoteAddr)
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
