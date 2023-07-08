package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
)

func getServerPubKey(iface, serverIP string) (string, error) {
	script := fmt.Sprintf("wg show %s allowed-ips | grep %s | cut -f1", iface, serverIP)
	outBytes, err := exec.Command("bash", "-c", script).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(outBytes)), nil
}

func getPeerList(iface string) []string {
	outBytes, err := exec.Command("wg", "show", iface, "peers").Output()
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(outBytes)), "\n")
}

func getExternalEndpoint(iface, serverIP, peer string) string {
	escapedPeer := url.QueryEscape(peer)
	request := fmt.Sprintf("http://%s?pubkey=%s", serverIP, escapedPeer)
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
	return string(respBytes)
}

func setPeer(iface, peer, endpoint string) error {
	log.Printf("setting %s endpoint to %s", peer, endpoint)

	return exec.Command("wg", "set", iface, "peer", peer,
		"persistent-keepalive", "25",
		"endpoint", endpoint,
	).Run()
}

func resolvePeers(iface, serverIP, serverPubKey string) {
	for _, p := range getPeerList(iface) {
		if p == serverPubKey {
			log.Printf("skipping %s", p)
			continue
		}
		endpoint := getExternalEndpoint(iface, serverIP, p)
		if endpoint != "(none)" {
			setPeer(iface, p, endpoint)
		}
	}
}

func main() {
	if len(os.Args) < 2 {
		log.Fatalf("usage: %s SERVER_IP [WG_IFACE]", os.Args[0])
	}

	serverIP := os.Args[1]
	iface := "wg0"
	if len(os.Args) > 2 {
		iface = os.Args[2]
	}
	serverPubKey, err := getServerPubKey(iface, serverIP)
	if err != nil {
		log.Fatal(err)
	}
	// fmt.Println(serverPubKey)

	resolvePeers(iface, serverIP, serverPubKey)
}
