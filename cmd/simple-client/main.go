package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

type WgClient struct {
	client   *wgctrl.Client
	iface    string
	serverIP string
}

func NewWgClient(iface, serverIP string) (*WgClient, error) {
	client, err := wgctrl.New()
	if err != nil {
		return nil, err
	}
	return &WgClient{
		client:   client,
		iface:    iface,
		serverIP: serverIP,
	}, nil
}

func (c *WgClient) getServerPubKey() (string, error) {
	serverIP := strings.Split(c.serverIP, ":")[0]

	dev, err := c.client.Device(c.iface)
	if err != nil {
		return "", err
	}

	for _, p := range dev.Peers {
		for _, ip := range p.AllowedIPs {
			if serverIP == ip.IP.String() {
				return p.PublicKey.String(), nil
			}
		}
	}
	return "", fmt.Errorf("no peer corresponding to the server")
}

func (c *WgClient) getPeerList() []string {
	dev, err := c.client.Device(c.iface)
	if err != nil {
		return nil
	}

	var peerPubKeys []string
	for _, p := range dev.Peers {
		peerPubKeys = append(peerPubKeys, p.PublicKey.String())
	}
	return peerPubKeys
}

func (c *WgClient) getExternalEndpoint(peer string) string {
	escapedPeer := url.QueryEscape(peer)
	request := fmt.Sprintf("http://%s?pubkey=%s", c.serverIP, escapedPeer)
	resp, err := http.Get(request)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(respBytes))
}

func (c *WgClient) setPeer(peer, endpoint string) error {
	endpointAddrPort, err := netip.ParseAddrPort(endpoint)
	if err != nil {
		return err
	}
	endpointUDPAddr := net.UDPAddrFromAddrPort(endpointAddrPort)
	keepalive := 25 * time.Second

	pubKey, err := wgtypes.ParseKey(peer)
	if err != nil {
		return err
	}

	log.Printf("setting %s endpoint to %s\n", peer, endpoint)

	return c.client.ConfigureDevice(c.iface, wgtypes.Config{
		Peers: []wgtypes.PeerConfig{
			{
				PublicKey:                   pubKey,
				Endpoint:                    endpointUDPAddr,
				PersistentKeepaliveInterval: &keepalive,
			},
		},
	})
}

func (c *WgClient) resolvePeers(serverPubKey string) {
	for _, p := range c.getPeerList() {
		if p == serverPubKey {
			log.Printf("skipping %s", p)
			continue
		}
		endpoint := c.getExternalEndpoint(p)
		if endpoint != "" {
			err := c.setPeer(p, endpoint)
			if err != nil {
				log.Printf("error configuring peer: %v", err)
			}
		}
	}
}

func main() {
	if len(os.Args) < 2 {
		log.Fatalf("usage: %s SERVER_IP[:PORT] [WG_IFACE]", os.Args[0])
	}

	serverIP := os.Args[1]
	if !strings.Contains(serverIP, ":") {
		serverIP += ":8080"
	}

	iface := "wg0"
	if len(os.Args) > 2 {
		iface = os.Args[2]
	}

	client, err := NewWgClient(iface, serverIP)
	if err != nil {
		log.Fatal(err)
	}

	serverPubKey, err := client.getServerPubKey()
	if err != nil {
		log.Fatal(err)
	}
	// fmt.Println(serverPubKey)

	client.resolvePeers(serverPubKey)
}
