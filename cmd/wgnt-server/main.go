package main

import (
	"encoding/json"
	"log"
	"net"
	"net/http"

	"github.com/nohajc/wg-nat-traversal/common/nat"
)

var peerTable = map[string]nat.STUNInfo{}

func requestHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		ip := r.URL.Query().Get("ip")
		log.Printf("GET request with ip = %s", ip)
		if peer, ok := peerTable[ip]; ok {
			enc := json.NewEncoder(w)
			err := enc.Encode(&peer)
			if err != nil {
				log.Printf("json encode error: %v", err)
				st := http.StatusInternalServerError
				http.Error(w, http.StatusText(st), st)
				return
			}
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
	case http.MethodPost:
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			log.Printf("host port split error: %v", err)
			st := http.StatusInternalServerError
			http.Error(w, http.StatusText(st), st)
			return
		}
		log.Printf("POST request from ip = %s", ip)

		info := nat.STUNInfo{}
		err = json.NewDecoder(r.Body).Decode(&info)
		if err != nil {
			log.Printf("json decode error: %v", err)
			st := http.StatusBadRequest
			http.Error(w, http.StatusText(st), st)
			return
		}

		// info.PublicIP = ip // TODO: uncomment after testing
		peerTable[info.PublicIP] = info
	}
}

func main() {
	http.HandleFunc("/", requestHandler)
	log.Fatal(http.ListenAndServe(":8080", nil))
}
