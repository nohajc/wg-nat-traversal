package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/nohajc/wg-nat-traversal/common/nat"

	"github.com/gorilla/websocket"
)

type Entry struct {
	Value  nat.STUNInfo
	Expiry *time.Timer
}

var upgrader = websocket.Upgrader{}

var peerTable = map[string]*Entry{}
var peerTableMu sync.Mutex

var pongWait = 30 * time.Second
var pingInterval = pongWait * 2 / 3

type WebSockRouter struct {
	clients   map[string]*Client
	clientsMu sync.RWMutex
}

func NewWebSockRouter() *WebSockRouter {
	return &WebSockRouter{
		clients: map[string]*Client{},
	}
}

func (wsr *WebSockRouter) AddClient(pubKey string, c *Client) {
	wsr.clientsMu.Lock()
	wsr.clients[pubKey] = c
	wsr.clientsMu.Unlock()
}

func (wsr *WebSockRouter) RemoveClient(pubKey string) {
	wsr.clientsMu.Lock()
	defer wsr.clientsMu.Unlock()

	c, ok := wsr.clients[pubKey]
	if !ok {
		return
	}

	close(c.writeChan)
	if err := c.conn.Close(); err != nil {
		log.Printf("error closing socket: %v", err)
	}

	delete(wsr.clients, pubKey)
}

type Client struct {
	pubKey    string
	conn      *websocket.Conn
	router    *WebSockRouter
	writeChan chan WriteRequest
}

func NewClient(pubKey string, conn *websocket.Conn, router *WebSockRouter) *Client {
	return &Client{
		pubKey:    pubKey,
		conn:      conn,
		router:    router,
		writeChan: make(chan WriteRequest, 4096),
	}
}

type WriteRequest struct {
	message    Message
	statusChan chan error
}

func MakeWriteRequest(msg Message) WriteRequest {
	return WriteRequest{
		message:    msg,
		statusChan: make(chan error, 1),
	}
}

func (r *WriteRequest) Error() chan error {
	return r.statusChan
}

type Message struct {
	// TODO
}

func (m *Message) String() string {
	return "" // TODO
}

func (c *Client) readMessage() (Message, error) {
	msg := Message{}
	err := c.conn.ReadJSON(&msg)
	return msg, err
}

func (c *Client) readIncoming() {
	defer c.router.RemoveClient(c.pubKey)

	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(appData string) error {
		log.Println("pong")
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	for {
		msg, err := c.readMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("socket read error: %v", err)
			}
			break
		}
		log.Printf("Message: %s", &msg)
	}
}

func (c *Client) writeMessage(ctx context.Context, msg Message) error {
	req := MakeWriteRequest(msg)
	c.writeChan <- req

	select {
	case err := <-req.Error():
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Client) writeOutgoing() {
	ticker := time.NewTicker(pingInterval)
	defer func() {
		ticker.Stop()
		c.router.RemoveClient(c.pubKey)
	}()

	for {
		select {
		case wReq, ok := <-c.writeChan:
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, nil)
				return
			}
			err := c.conn.WriteJSON(&wReq.message)
			wReq.statusChan <- err

		case <-ticker.C:
			log.Println("ping")
			err := c.conn.WriteMessage(websocket.PingMessage, nil)
			if err != nil {
				log.Printf("socket write error: %v", err)
				return
			}
		}
	}
}

func (wsr *WebSockRouter) wsRequestHandler(w http.ResponseWriter, r *http.Request) {
	pubKey := r.URL.Query().Get("pubkey")
	if pubKey == "" {
		st := http.StatusBadRequest
		http.Error(w, http.StatusText(st), st)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("socket upgrade failed: %v", err)
		return
	}

	c := NewClient(pubKey, conn, wsr)
	go c.readIncoming()
	go c.writeOutgoing()
}

func (wsr *WebSockRouter) requestHandler(w http.ResponseWriter, r *http.Request) {
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
			wsPeer, ok := wsr.clients[pubKey]
			if ok {
				err := wsPeer.writeMessage(r.Context(), Message{})
				if err == nil {
					// TODO: wait for response from peer
					// which will announce the port mapping
					log.Printf("notified peer %s", pubKey)
				} else {
					log.Printf("failed to notify peer %s: %v", pubKey, err)
				}
			}
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
	wsr := NewWebSockRouter()
	http.HandleFunc("/", wsr.requestHandler)
	http.HandleFunc("/ws", wsr.wsRequestHandler)
	log.Fatal(http.ListenAndServe(":8080", nil))
}
