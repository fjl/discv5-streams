package fileserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/fjl/discv5-streams/host"
	"github.com/fjl/discv5-streams/session"
)

const (
	// This is how long the client will wait for an xfer-start request from the server.
	transferStartTimeout = 10 * time.Second
)

var (
	errClientClosed             = errors.New("client closed")
	errCanceled                 = errors.New("transfer canceled")
	errRejectedByServer         = errors.New("server rejected transfer")
	errTransferHandshakeTimeout = errors.New("transfer handshake timeout")
)

type Client struct {
	cfg  *Config
	host *host.Host

	wg     sync.WaitGroup
	quit   chan struct{}
	create chan clientCreateEv
	cancel chan clientCancelEv
	init   chan clientInitEv
	start  chan clientStartEv
}

type clientTransfer struct {
	createTime  time.Time
	acceptStart chan *clientTransfer
	started     chan *clientTransfer
	session     *session.Session
	err         error
}

type transferKey struct {
	node enode.ID
	id   uint16
}

// loop() event types
type (
	clientCreateEv struct {
		node    enode.ID
		id      uint16
		started chan *clientTransfer
	}

	clientCancelEv struct {
		node enode.ID
		id   uint16
	}

	clientInitEv struct {
		node enode.ID
		id   uint16
		resp xferInitResponse
	}

	clientStartEv struct {
		node   enode.ID
		req    xferStartRequest
		accept chan *clientTransfer
	}
)

func NewClient(host *host.Host, cfg Config) *Client {
	cfg = cfg.withDefaults()
	c := &Client{
		host:   host,
		cfg:    &cfg,
		quit:   make(chan struct{}),
		create: make(chan clientCreateEv),
		init:   make(chan clientInitEv),
		start:  make(chan clientStartEv),
	}
	c.wg.Add(1)
	go c.loop()

	xferStart := cfg.Prefix + "-start"
	host.Discovery.RegisterTalkHandler(xferStart, c.handleXferStart)
	return c
}

// Request fetches a file from the given node.
func (c *Client) Request(ctx context.Context, node *enode.Node, file string) (io.ReadCloser, error) {
	create := clientCreateEv{
		id:      c.generateID(),
		node:    node.ID(),
		started: make(chan *clientTransfer, 1),
	}
	if !clientEvent(c, c.create, create) {
		return nil, errClientClosed
	}
	if err := c.sendXferInit(node, file, create.id); err != nil {
		clientEvent(c, c.cancel, clientCancelEv{node.ID(), create.id})
		return nil, err
	}

	// Wait for the transfer to be started by the server.
	select {
	case t := <-create.started:
		if t.err != nil {
			return nil, t.err
		}
		// return t.session, nil
		return nil, nil
	case <-ctx.Done():
		clientEvent(c, c.cancel, clientCancelEv{node.ID(), create.id})
		return nil, ctx.Err()
	}
}

func (c *Client) generateID() uint16 {
	return uint16(rand.Intn(math.MaxUint16))
}

func clientEvent[T any](c *Client, ch chan<- T, ev T) bool {
	select {
	case ch <- ev:
		return true
	case <-c.quit:
		return false
	}
}

func (c *Client) Close() {
	close(c.quit)
	c.wg.Wait()
}

func (c *Client) loop() {
	defer c.wg.Done()

	var (
		transfers = make(map[transferKey]*clientTransfer)
		ticker    = time.NewTicker(10 * time.Second)
	)

	for {
		select {
		case create := <-c.create:
			key := transferKey{create.node, create.id}
			transfers[key] = &clientTransfer{
				started: create.started,
			}

		case cancel := <-c.cancel:
			key := transferKey{cancel.node, cancel.id}
			t := transfers[key]
			if t != nil {
				t.err = errCanceled
				if t.acceptStart != nil {
					t.acceptStart <- nil
				}
				delete(transfers, key)
			}

		case init := <-c.init:
			key := transferKey{init.node, init.id}
			t := transfers[key]
			if t == nil {
				continue
			}
			if !init.resp.OK {
				t.err = errRejectedByServer
				delete(transfers, key)
				continue
			}
			// It was accepted. Since the init response and start request are not
			// synchronized, the start request might have arrived already. Unblock the
			// start request handler if it did.
			if t.acceptStart != nil {
				t.acceptStart <- t
			}

		case start := <-c.start:
			key := transferKey{start.node, start.req.ID}
			t := transfers[key]
			if t == nil {
				continue
			}
			t.acceptStart = start.accept
			if t.err == nil {
				t.acceptStart <- t
			}

		case <-ticker.C:
			now := time.Now()
			for key, t := range transfers {
				if t.createTime.Add(transferStartTimeout).After(now) {
					t.err = errTransferHandshakeTimeout
					delete(transfers, key)
				}
			}

		case <-c.quit:
			return
		}
	}
}

func (c *Client) sendXferInit(node *enode.Node, file string, id uint16) error {
	req := &xferInitRequest{Filename: file, ID: id}
	reqBytes, _ := rlp.EncodeToBytes(req)
	xferInit := c.cfg.Prefix + "-init"
	respBytes, err := c.host.Discovery.TalkRequest(node, xferInit, reqBytes)
	if err != nil {
		return err
	}

	var resp xferInitResponse
	if err := rlp.DecodeBytes(respBytes, &resp); err != nil {
		return fmt.Errorf("invalid response: %v", err)
	}
	c.init <- clientInitEv{node.ID(), id, resp}
	if !resp.OK {
		return fmt.Errorf("server rejected transfer")
	}
	return nil
}

func (c *Client) handleXferStart(node enode.ID, addr *net.UDPAddr, reqBytes []byte) []byte {
	var req xferStartRequest
	if err := rlp.DecodeBytes(reqBytes, &req); err != nil {
		return nil
	}

	accept := make(chan *clientTransfer, 1)
	c.start <- clientStartEv{node, req, accept}

	var transfer *clientTransfer
	timeoutTimer := time.NewTimer(400 * time.Millisecond)
	defer timeoutTimer.Stop()
	select {
	case transfer = <-accept:
	case <-timeoutTimer.C:
		fmt.Println("accept timeout")
	}
	if transfer == nil {
		// Canceled or timed out.
		return encodeXferStartResponse(false, [16]byte{})
	}

	// Relay accept signal to the waiting caller.
	defer func() { transfer.started <- transfer }()

	ip, _ := netip.AddrFromSlice(addr.IP)
	session, recipientSecret, err := c.host.SessionStore.Recipient(ip, c.cfg.Prefix, req.InitiatorSecret)
	if err != nil {
		transfer.err = fmt.Errorf("session establishment failed: %v", err)
		return encodeXferStartResponse(false, [16]byte{})
	}
	transfer.session = session
	return encodeXferStartResponse(true, recipientSecret)
}

func encodeXferStartResponse(ok bool, recipientSecret [16]byte) []byte {
	resp := &xferStartResponse{ok, recipientSecret}
	respBytes, _ := rlp.EncodeToBytes(resp)
	return respBytes
}
