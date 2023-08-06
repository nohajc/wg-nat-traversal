package wireguard

import (
	"errors"
	"fmt"
	"net"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
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

// returns peer's public key
func (wg *WgClient) FindPeerByRemoteIP(remoteIP string) (string, error) {
	dev, err := wg.client.Device(wg.iface)
	if err != nil {
		return "", err
	}

	for _, p := range dev.Peers {
		if p.Endpoint.IP.String() == remoteIP {
			return p.PublicKey.String(), nil
		}
	}
	return "", errors.New("peer not found")
}

func (wg *WgClient) SetListenPort(listenPort int) error {
	return wg.client.ConfigureDevice(wg.iface, wgtypes.Config{
		ListenPort: &listenPort,
	})
}

func (wg *WgClient) SetPeerRemotePort(peer string, remoteIP string, remotePort int) error {
	pubKey, err := wgtypes.ParseKey(peer)
	if err != nil {
		return err
	}

	endpointUDPAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", remoteIP, remotePort))
	if err != nil {
		return err
	}
	keepalive := 25 * time.Second

	return wg.client.ConfigureDevice(wg.iface, wgtypes.Config{
		Peers: []wgtypes.PeerConfig{{
			PublicKey:                   pubKey,
			Endpoint:                    endpointUDPAddr,
			PersistentKeepaliveInterval: &keepalive,
		}},
	})
}
