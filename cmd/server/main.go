package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"golang.zx2c4.com/wireguard/wgctrl"
)

type WgClient struct {
	client *wgctrl.Client
	iface  string
}

func NewWgClient(iface string) (*WgClient, error) {
	client, err := wgctrl.New()
	if err != nil {
		return nil, err
	}
	return &WgClient{
		client: client,
		iface:  iface,
	}, nil
}

func (c *WgClient) getEndpoint(peer string) string {
	dev, err := c.client.Device(c.iface)
	if err != nil {
		return ""
	}
	for _, p := range dev.Peers {
		if p.PublicKey.String() == peer {
			if p.Endpoint == nil {
				return ""
			}
			return p.Endpoint.String()
		}
	}
	return ""
}

func makeHandler(c *WgClient) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		pubKey := r.URL.Query().Get("pubkey")
		endpoint := c.getEndpoint(pubKey)
		_, _ = fmt.Fprintln(w, endpoint)
	}
}

func main() {
	iface := "wg0"
	if len(os.Args) > 1 {
		iface = os.Args[1]
	}
	client, err := NewWgClient(iface)
	if err != nil {
		log.Fatal(err)
	}
	http.HandleFunc("/", makeHandler(client))
	log.Fatal(http.ListenAndServe(":8080", nil))
}
