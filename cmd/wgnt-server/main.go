package main

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/nohajc/wg-nat-traversal/common/nat"
)

type Entry struct {
	Value  nat.STUNInfo
	Expiry *time.Timer
}

var peerTable = map[string]*Entry{}
var peerTableMu sync.Mutex

func requestHandler(w http.ResponseWriter, r *http.Request) {
	pubKey := r.URL.Query().Get("pubkey")
	if pubKey == "" {
		st := http.StatusBadRequest
		http.Error(w, http.StatusText(st), st)
		return
	}

	switch r.Method {
	case http.MethodGet:
		log.Printf("GET request with pubkey = %s", pubKey)

		peerTableMu.Lock()
		peer, ok := peerTable[pubKey]
		peerTableMu.Unlock()

		if ok {
			enc := json.NewEncoder(w)
			err := enc.Encode(&peer.Value)
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
		if entry, ok := peerTable[pubKey]; ok {
			if entry.Value != info {
				entry.Expiry.Reset(20 * time.Second)
				entry.Value = info
			}
		} else {
			expiry := time.AfterFunc(20*time.Second, func() {
				peerTableMu.Lock()
				delete(peerTable, pubKey)
				log.Printf("deleted %s from table", pubKey)
				peerTableMu.Unlock()
			})

			peerTable[pubKey] = &Entry{
				Value:  info,
				Expiry: expiry,
			}
		}
		peerTableMu.Unlock()
	}
}

func main() {
	http.HandleFunc("/", requestHandler)
	log.Fatal(http.ListenAndServe(":8080", nil))
}
