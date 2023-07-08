package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
)

func makeHandler(iface string) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		pubKey := r.URL.Query().Get("pubkey")
		script := fmt.Sprintf("wg show %s endpoints | grep %s | cut -f2", iface, pubKey)
		outBytes, err := exec.Command("bash", "-c", script).Output()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_, _ = w.Write(outBytes)
	}
}

func main() {
	iface := "wg0"
	if len(os.Args) > 1 {
		iface = os.Args[1]
	}
	http.HandleFunc("/", makeHandler(iface))
	log.Fatal(http.ListenAndServe(":80", nil))
}
