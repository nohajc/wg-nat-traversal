package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/nohajc/wg-nat-traversal/common/utils"
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

func (c *Client) PublishPeerInfo(info *utils.STUNInfo) error {
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

func (c *Client) GetPeerInfo(peerHost string) (*utils.STUNInfo, error) {
	resp, err := http.Get(fmt.Sprintf("%s?ip=%s", c.ServerURL, peerHost))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	result := &utils.STUNInfo{}
	dec := json.NewDecoder(resp.Body)
	err = dec.Decode(result)
	return result, err
}

func (c *Client) WaitForPeerInfo(peerHost string) (*utils.STUNInfo, error) {
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

	conn, err := newConn()
	if err != nil {
		log.Fatalf("connection error: %v", err)
	}
	defer conn.Close()

	stunInfo, err := utils.GetPublicAddrWithNATKind(conn)
	if err != nil {
		log.Fatalf("STUN error: %v", err)
	}

	fmt.Printf("NAT type: %s\n", stunInfo.NATKind)
	if stunInfo.NATKind == utils.NAT_EASY {
		fmt.Printf("%s -> %s:%d\n", conn.LocalAddr().String(), stunInfo.PublicIP, stunInfo.PublicPort)
	} else {
		fmt.Printf("%s -> %s:?\n", conn.LocalAddr().String(), stunInfo.PublicIP)
	}

	client := NewClient(serverHost)
	err = client.PublishPeerInfo(stunInfo)
	if err != nil {
		log.Fatalf("Server error: %v", err)
	}

	peerInfo, err := client.WaitForPeerInfo(peerHost)
	if err != nil {
		log.Fatalf("Server error: %v", err)
	}
	fmt.Printf("%s:%d - NAT: %s\n", peerInfo.PublicIP, peerInfo.PublicPort, peerInfo.NATKind)
}
