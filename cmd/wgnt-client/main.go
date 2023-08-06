package main

import (
	"fmt"
	"log"
	"net"

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

func main() {
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
	}

	// ip, port, err := utils.GetPublicAddr(conn)
	// if err != nil {
	// 	log.Fatal(err)
	// }
	// fmt.Printf("%s:%d\n", ip, port)
}
