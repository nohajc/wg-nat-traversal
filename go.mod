module github.com/nohajc/wg-nat-traversal

go 1.20

require (
	github.com/pion/transport/v2 v2.2.1
	golang.zx2c4.com/wireguard/wgctrl v0.0.0-20230429144221-925a1e7659e6
)

require (
	github.com/pion/dtls/v2 v2.2.7 // indirect
	github.com/pion/logging v0.2.2 // indirect
)

require (
	github.com/google/go-cmp v0.5.9 // indirect
	github.com/gorilla/websocket v1.5.0
	github.com/josharian/native v1.1.0 // indirect
	github.com/mdlayher/genetlink v1.3.2 // indirect
	github.com/mdlayher/netlink v1.7.2 // indirect
	github.com/mdlayher/socket v0.4.1 // indirect
	github.com/pion/stun v0.6.1
	golang.org/x/crypto v0.8.0 // indirect
	golang.org/x/net v0.9.0 // indirect
	golang.org/x/sync v0.1.0 // indirect
	golang.org/x/sys v0.7.0 // indirect
	golang.zx2c4.com/wireguard v0.0.0-20230325221338-052af4a8072b // indirect
)

replace golang.zx2c4.com/wireguard/wgctrl => github.com/nohajc/wgctrl-go v0.0.0-20230909120350-ad59fbf5267b
