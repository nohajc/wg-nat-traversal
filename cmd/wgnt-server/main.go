package main

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/nohajc/wg-nat-traversal/common/nat"
)

var peerTable = map[string]nat.STUNInfo{}
var peerTableMu sync.Mutex

func requestHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		ip := r.URL.Query().Get("ip")
		log.Printf("GET request with ip = %s", ip)

		peerTableMu.Lock()
		peer, ok := peerTable[ip]
		peerTableMu.Unlock()

		if ok {
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
		peerTableMu.Lock()
		peerTable[info.PublicIP] = info
		peerTableMu.Unlock()

		time.AfterFunc(20*time.Second, func() {
			peerTableMu.Lock()
			delete(peerTable, info.PublicIP)
			peerTableMu.Unlock()
		})
	}
}

func main() {
	http.HandleFunc("/", requestHandler)
	log.Fatal(http.ListenAndServe(":8080", nil))
}
