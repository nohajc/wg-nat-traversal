package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/gorilla/websocket"
	"github.com/nohajc/wg-nat-traversal/common/nat"
	"github.com/nohajc/wg-nat-traversal/common/wireguard"
)

func newConn() (*net.UDPConn, error) {
	localAddr, err := net.ResolveUDPAddr("udp", ":0")
	if err != nil {
		return nil, err
	}

	conn, err := net.ListenUDP("udp", localAddr)
	if err != nil {
		return nil, err
	}

	return conn, nil
}

type Client struct {
	ServerURL string
}

func NewClient(serverHost string) *Client {
	return &Client{
		ServerURL: fmt.Sprintf("http://%s:8080/", serverHost),
	}
}

func (c *Client) PublishPeerInfo(pubKey string, info *nat.STUNInfo) error {
	reqPayload, err := json.Marshal(info)
	if err != nil {
		return err
	}
	resp, err := http.Post(
		fmt.Sprintf("%s?pubkey=%s", c.ServerURL, pubKey),
		"application/json", bytes.NewReader(reqPayload),
	)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected http status: %s", resp.Status)
	}
	return nil
}

func (c *Client) GetPeerInfo(peerPubKey string) (*nat.STUNInfo, error) {
	resp, err := http.Get(fmt.Sprintf("%s?pubkey=%s", c.ServerURL, peerPubKey))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	result := &nat.STUNInfo{}
	dec := json.NewDecoder(resp.Body)
	err = dec.Decode(result)
	return result, err
}

func (c *Client) WaitForPeerInfo(peerPubKey string) (*nat.STUNInfo, error) {
	for {
		result, err := c.GetPeerInfo(peerPubKey)
		if err != nil {
			return nil, err
		}
		if result != nil {
			return result, nil
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func setWireguardPorts(wgClient *wireguard.WgClient, peerPubKey string, params *STUNParams) error {
	fmt.Println("setWireguardPorts:")
	fmt.Printf("- peer: %s:%d\n", params.remote.PublicIP, params.remote.PublicPort)
	fmt.Printf("- local listen port: %d\n", params.localPrivPort)

	err := wgClient.SetPeerRemotePort(peerPubKey, params.remote.PublicIP, params.remote.PublicPort)
	if err != nil {
		return err
	}

	err = wgClient.SetListenPort(params.localPrivPort)
	if err != nil {
		return err
	}

	return nil
}

type STUNParams struct {
	localPrivPort int
	remote        nat.STUNInfo
}

func resolvePorts(wgClient *wireguard.WgClient, peerPubKey string, serverHost string) (*STUNParams, error) {
	conn, err := newConn()
	if err != nil {
		return nil, fmt.Errorf("connection error: %w", err)
	}
	defer conn.Close()

	stunInfo, err := nat.GetPublicAddrWithNATKind(conn)
	if err != nil {
		return nil, fmt.Errorf("STUN error: %w", err)
	}

	fmt.Printf("NAT type: %s\n", stunInfo.NATKind)
	if stunInfo.NATKind == nat.NAT_EASY {
		fmt.Printf("%s -> %s:%d\n", conn.LocalAddr().String(), stunInfo.PublicIP, stunInfo.PublicPort)
	} else {
		fmt.Printf("%s -> %s:?\n", conn.LocalAddr().String(), stunInfo.PublicIP)
	}

	pubKey, err := wgClient.GetInterfacePublicKey()
	if err != nil {
		return nil, fmt.Errorf("error getting wg interface public key: %w", err)
	}

	client := NewClient(serverHost)
	err = client.PublishPeerInfo(pubKey, stunInfo)
	if err != nil {
		return nil, fmt.Errorf("server error: %w", err)
	}

	peerInfo, err := client.WaitForPeerInfo(peerPubKey)
	if err != nil {
		return nil, fmt.Errorf("server error: %w", err)
	}
	fmt.Printf("peer %s:%d - NAT type: %s\n", peerInfo.PublicIP, peerInfo.PublicPort, peerInfo.NATKind)

	if stunInfo.NATKind == nat.NAT_HARD && peerInfo.NATKind == nat.NAT_HARD {
		return nil, errors.New("both peers are behind symmetric NAT, hole punching not feasible; exiting")
	}

	localPrivPort := conn.LocalAddr().(*net.UDPAddr).Port

	if stunInfo.NATKind == nat.NAT_HARD || peerInfo.NATKind == nat.NAT_HARD {
		if stunInfo.NATKind == nat.NAT_EASY {
			remotePort, err := nat.GuessRemotePort(
				peerInfo.PublicIP, nat.WithConn(conn),
				nat.WithPubAddr(stunInfo.PublicIP, stunInfo.PublicPort),
			)
			if err != nil {
				return nil, fmt.Errorf("guess remote port error: %w", err)
			}
			peerInfo.PublicPort = remotePort
		} else {
			localPort, err := nat.GuessLocalPort(
				fmt.Sprintf("%s:%d", peerInfo.PublicIP, peerInfo.PublicPort),
			)
			if err != nil {
				return nil, fmt.Errorf("guess local port error: %w", err)
			}
			localPrivPort = localPort
		}
	}
	// else EASY && EASY - nothing to do, ports already correct

	return &STUNParams{
		localPrivPort: localPrivPort,
		remote:        *peerInfo,
	}, nil
}

func main() {
	var serverHost, wgDevice string
	var daemonMode bool // should be used by the peer with a wireguard server

	flag.BoolVar(&daemonMode, "d", false, "daemon mode (listen for peers)")
	flag.StringVar(&serverHost, "s", "", "server IP/hostname")
	flag.StringVar(&wgDevice, "w", "", "Wireguard interface")
	flag.Parse()

	if serverHost == "" {
		fmt.Fprintln(os.Stderr, "missing server IP/hostname")
		os.Exit(1)
	}
	if wgDevice == "" {
		fmt.Fprintln(os.Stderr, "missing Wireguard interface")
		os.Exit(1)
	}

	wgClient, err := wireguard.NewWgClient(wgDevice)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	peers, err := wgClient.GetPeers()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	if len(peers) < 1 {
		fmt.Fprintln(os.Stderr, "at least one Peer required in wg config")
		os.Exit(1)
	}
	peerPubKey := peers[0].PublicKey.String()

	if daemonMode {
		wsURL := fmt.Sprintf("ws://%s:8080/ws?pubkey=%s", serverHost, peerPubKey)
		wsConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}

		for {
			var msg struct{}
			err := wsConn.ReadJSON(&msg)
			if err != nil {
				log.Println("read:", err)
				return
			}
			log.Println("received message")
		}
	}

	params, err := resolvePorts(wgClient, peerPubKey, serverHost)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	err = setWireguardPorts(wgClient, peerPubKey, params)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}
