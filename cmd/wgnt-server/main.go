package main

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/nohajc/wg-nat-traversal/common/nat"
)

var peerTable = map[string]nat.STUNInfo{}
var peerTableMu sync.Mutex

func requestHandler(w http.ResponseWriter, r *http.Request) {
	pubKey := r.URL.Query().Get("pubkey")
	switch r.Method {
	case http.MethodGet:
		log.Printf("GET request with pubkey = %s", pubKey)

		peerTableMu.Lock()
		peer, ok := peerTable[pubKey]
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
		log.Printf("POST request with pubkey = %s", pubKey)

		info := nat.STUNInfo{}
		err := json.NewDecoder(r.Body).Decode(&info)
		if err != nil {
			log.Printf("json decode error: %v", err)
			st := http.StatusBadRequest
			http.Error(w, http.StatusText(st), st)
			return
		}

		peerTableMu.Lock()
		peerTable[pubKey] = info
		peerTableMu.Unlock()

		// TODO: proper TTL - this is incorrect if an existing key is updated
		time.AfterFunc(20*time.Second, func() {
			peerTableMu.Lock()
			delete(peerTable, pubKey)
			peerTableMu.Unlock()
		})
	}
}

func main() {
	http.HandleFunc("/", requestHandler)
	log.Fatal(http.ListenAndServe(":8080", nil))
}
