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

func (c *WgClient) GetInterfacePublicKey() (string, error) {
	dev, err := c.client.Device(c.iface)
	if err != nil {
		return "", err
	}

	return dev.PublicKey.String(), nil
}

func (c *WgClient) GetPeers() []wgtypes.Peer {
	dev, err := c.client.Device(c.iface)
	if err != nil {
		return nil
	}

	return dev.Peers
}

// func (wg *WgClient) FindPeerByPublicKey(pubKey string) (wgtypes.Peer, error) {
// 	dev, err := wg.client.Device(wg.iface)
// 	if err != nil {
// 		return wgtypes.Peer{}, err
// 	}

// 	for _, p := range dev.Peers {
// 		if p.PublicKey.String() == pubKey {
// 			return p, nil
// 		}
// 	}
// 	return wgtypes.Peer{}, errors.New("peer not found")
// }

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

func (wg *WgClient) SetPeerRemotePort(peerPubKey string, remoteIP string, remotePort int) error {
	pubKey, err := wgtypes.ParseKey(peerPubKey)
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
