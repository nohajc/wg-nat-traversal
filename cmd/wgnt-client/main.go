package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/nohajc/wg-nat-traversal/common/nat"
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

func (c *Client) PublishPeerInfo(info *nat.STUNInfo) error {
	reqPayload, err := json.Marshal(info)
	if err != nil {
		return err
	}
	resp, err := http.Post(c.ServerURL, "application/json", bytes.NewReader(reqPayload))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected http status: %s", resp.Status)
	}
	return nil
}

func (c *Client) GetPeerInfo(peerHost string) (*nat.STUNInfo, error) {
	resp, err := http.Get(fmt.Sprintf("%s?ip=%s", c.ServerURL, peerHost))
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

func (c *Client) WaitForPeerInfo(peerHost string) (*nat.STUNInfo, error) {
	for {
		result, err := c.GetPeerInfo(peerHost)
		if err != nil {
			return nil, err
		}
		if result != nil {
			return result, nil
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func setWireguardPorts(peerIP string, peerPort int, listenPort int) error {
	fmt.Printf("local listen port: %d\n", listenPort)
	return nil // TODO
}

type STUNParams struct {
	localPrivPort int
	local         nat.STUNInfo
	remote        nat.STUNInfo
}

func resolvePorts(peerHost, serverHost string) (*STUNParams, error) {
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

	client := NewClient(serverHost)
	err = client.PublishPeerInfo(stunInfo)
	if err != nil {
		return nil, fmt.Errorf("server error: %w", err)
	}

	peerInfo, err := client.WaitForPeerInfo(peerHost)
	if err != nil {
		return nil, fmt.Errorf("server error: %w", err)
	}
	fmt.Printf("%s:%d - NAT type: %s\n", peerInfo.PublicIP, peerInfo.PublicPort, peerInfo.NATKind)

	if stunInfo.NATKind == nat.NAT_HARD && peerInfo.NATKind == nat.NAT_HARD {
		return nil, errors.New("both peers are behind symmetric NAT, hole punching not feasible; exiting")
	}

	if stunInfo.NATKind == nat.NAT_HARD || peerInfo.NATKind == nat.NAT_HARD {
		// TODO: guess remote or local port
	}

	// else EASY && EASY - nothing to do

	return &STUNParams{
		localPrivPort: conn.LocalAddr().(*net.UDPAddr).Port,
		local:         *stunInfo,
		remote:        *peerInfo,
	}, nil
}

func main() {
	var peerHost, serverHost string
	flag.StringVar(&peerHost, "p", "", "peer IP/hostname")
	flag.StringVar(&serverHost, "s", "", "server IP/hostname")
	flag.Parse()

	if peerHost == "" {
		fmt.Fprintf(os.Stderr, "missing PEER_IP")
		os.Exit(1)
	}
	if serverHost == "" {
		fmt.Fprintf(os.Stderr, "missing SERVER_IP")
		os.Exit(1)
	}

	params, err := resolvePorts(peerHost, serverHost)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v", err)
		os.Exit(1)
	}

	err = setWireguardPorts(params.remote.PublicIP, params.remote.PublicPort, params.localPrivPort)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v", err)
		os.Exit(1)
	}
}
