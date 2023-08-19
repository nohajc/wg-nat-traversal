package nat

import (
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net"
	"os"
	"sync/atomic"
	"time"

	"github.com/pion/stun"
	"github.com/pion/transport/v2"
	"github.com/pion/transport/v2/stdnet"
)

type STUNSrv string

const STUN_Google STUNSrv = "stun:stun.l.google.com:19302"
const STUN_Google1 STUNSrv = "stun:stun1.l.google.com:19302"
const STUN_VoipGATE STUNSrv = "stun:stun.voipgate.com:3478"

type NAT int

const (
	NAT_EASY NAT = iota
	NAT_HARD
)

var natNames = [...]string{"easy", "hard"}

func (n NAT) String() string {
	if int(n) >= len(natNames) {
		return ""
	}
	return natNames[n]
}

// MarshalText implements the encoding.TextMarshaler interface.
func (n NAT) MarshalText() ([]byte, error) {
	return []byte(n.String()), nil
}

// UnmarshalText implements the encoding.TextUnmarshaler interface.
func (n *NAT) UnmarshalText(b []byte) error {
	aux := string(b)
	for i, name := range natNames {
		if name == aux {
			*n = NAT(i)
			return nil
		}
	}
	return fmt.Errorf("invalid locality type %q", aux)
}

type ReusedConn struct {
	*net.UDPConn
	remoteAddr net.Addr
}

func (rc *ReusedConn) Write(b []byte) (int, error) {
	return rc.UDPConn.WriteTo(b, rc.remoteAddr)
}

func (rc *ReusedConn) Close() error {
	rc.UDPConn.SetReadDeadline(time.Now())
	return nil
}

type CustomNet struct {
	*stdnet.Net
	conn *net.UDPConn
}

func (cn *CustomNet) Dial(network string, address string) (net.Conn, error) {
	cn.conn.SetReadDeadline(time.Time{})

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

type STUNInfo struct {
	PublicIP   string `json:"public_ip"`
	PublicPort int    `json:"public_port"`
	NATKind    NAT    `json:"nat_kind"`
}

type Message struct {
	Test string `json:"test"`
}

func GetPublicAddrWithNATKind(conn *net.UDPConn) (*STUNInfo, error) {
	_, port1, err := STUN_Google.getPublicAddr(conn)
	if err != nil {
		return nil, err
	}

	ip, port2, err := STUN_VoipGATE.getPublicAddr(conn)
	if err != nil {
		return nil, err
	}

	natKind := NAT_EASY

	if port1 != port2 {
		natKind = NAT_HARD
	}

	return &STUNInfo{
		PublicIP:   ip,
		PublicPort: port1,
		NATKind:    natKind,
	}, nil
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
	defer client.Close()

	// Building binding request with random transaction id.
	message := stun.MustBuild(stun.TransactionID, stun.BindingRequest)

	var IP string
	var port int
	var cbErr error

	// Sending request to STUN server, waiting for response message.
	if err := client.Do(message, func(res stun.Event) {
		if res.Error != nil {
			cbErr = err
			return
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
				localPort := conn.LocalAddr().(*net.UDPAddr).Port

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

type clientCfg struct {
	conn        *net.UDPConn
	pubIP       string
	pubPort     int
	interactive bool
}

type Option func(*clientCfg)

func WithConn(conn *net.UDPConn) Option {
	return func(cc *clientCfg) {
		cc.conn = conn
	}
}

func WithPubAddr(pubIP string, pubPort int) Option {
	return func(cc *clientCfg) {
		cc.pubIP = pubIP
		cc.pubPort = pubPort
	}
}

func Interactive(i bool) Option {
	return func(cc *clientCfg) {
		cc.interactive = i
	}
}

func GuessRemotePort(remoteIP string, opts ...Option) (int, error) {
	var cc clientCfg
	for _, opt := range opts {
		opt(&cc)
	}
	conn := cc.conn

	if conn == nil {
		localAddr, err := net.ResolveUDPAddr("udp", ":0")
		if err != nil {
			return 0, err
		}

		conn, err = net.ListenUDP("udp", localAddr)
		if err != nil {
			return 0, err
		}
		defer conn.Close()
	}

	pubIP := cc.pubIP
	pubPort := cc.pubPort

	if len(pubIP) == 0 {
		var err error
		pubIP, pubPort, err = GetPublicAddr(conn)
		if err != nil {
			return 0, err
		}
	}

	if cc.interactive {
		fmt.Printf("%s -> %s:%d\n", conn.LocalAddr().String(), pubIP, pubPort)
		fmt.Println("Press Enter to continue")
		fmt.Scanln()
	}

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
		if c.LocalAddr().(*net.UDPAddr).Port == portInfo.LocalPort {
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
