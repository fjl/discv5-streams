package kcpxfer

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/p2p/discover"
	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/xtaci/kcp-go"
)

const (
	ecParityShards = 3
	ecDataShards   = 10
	minPacketSize  = len(ID{})
)

// ID is a transfer identifier. IDs are assigned based on the hash of the
// transferred item and the node it is being sent to.
type ID [16]byte

// TransferRequest represents a request for an incoming transfer.
type TransferRequest struct {
	Node enode.ID
	Addr *net.UDPAddr
	Hash [32]byte
	Size uint64

	mu           sync.Mutex
	accept       chan *xferState
	xfer         *xferState
	server       *Server
	timeoutTimer *time.Timer
}

func (tr *TransferRequest) Accept() (net.Conn, error) {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	if tr.xfer == nil {
		return nil, errors.New("already accepted / timed out")
	}
	conn := tr.xfer.session
	tr.doAccept(true)
	return conn, nil
}

func (tr *TransferRequest) Reject() {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	if tr.xfer == nil {
		return
	}
	tr.doAccept(false)
}

func (tr *TransferRequest) doAccept(accepted bool) {
	tr.timeoutTimer.Stop()
	if accepted {
		tr.accept <- tr.xfer
		tr.server.registerXfer <- tr.xfer
	} else {
		tr.accept <- nil
		tr.xfer.close()
	}
	tr.xfer = nil
}

// Protocol messages.
type (
	startRequest struct {
		Size uint64
		Hash [32]byte
	}

	startResponse struct {
		Accept bool
	}
)

func computeID(contentHash [32]byte, fromNode enode.ID) (id ID) {
	h := sha256.New()
	h.Write(contentHash[:])
	h.Write(fromNode[:])
	copy(id[:], h.Sum(nil))
	return id
}

type Server struct {
	disc             *discover.UDPv5
	conn             *net.UDPConn
	packet           <-chan discover.ReadPacket
	startAsRecipient chan *TransferRequest
	registerXfer     chan *xferState
	serveFunc        func(*TransferRequest) error
}

type ServerConfig struct {
	Handler   func(*TransferRequest) error
	Discovery *discover.UDPv5
	Conn      *net.UDPConn
	InChannel <-chan discover.ReadPacket
}

type xferState struct {
	id      ID
	conn    *kcpConn
	session *kcp.UDPSession
}

func (s *xferState) close() {
	s.session.Close()
}

func NewServer(cfg ServerConfig) *Server {
	if cfg.Discovery == nil || cfg.Conn == nil || cfg.InChannel == nil {
		panic("invalid config")
	}
	s := &Server{
		disc:             cfg.Discovery,
		conn:             cfg.Conn,
		packet:           cfg.InChannel,
		serveFunc:        cfg.Handler,
		startAsRecipient: make(chan *TransferRequest),
		registerXfer:     make(chan *xferState),
	}
	go s.loop()
	s.disc.RegisterTalkHandler("wrm", s.handleTalk)
	return s
}

// Transfer creates an outgoing transfer to the given node.
func (s *Server) Transfer(n *enode.Node, contentHash [32]byte, size int64) (net.Conn, error) {
	if n.IP() == nil && n.UDP() == 0 {
		return nil, fmt.Errorf("destination node has no UDP endpoint")
	}
	addr := &net.UDPAddr{IP: n.IP(), Port: n.UDP()}
	req := &startRequest{Hash: contentHash, Size: uint64(size)}
	err := s.requestTransfer(n, req)
	if err != nil {
		return nil, err
	}

	id := computeID(req.Hash, s.disc.Self().ID())
	xfer := s.newState(id, addr)
	s.registerXfer <- xfer
	return xfer.session, nil
}

func (s *Server) requestTransfer(n *enode.Node, req *startRequest) error {
	startmsg, err := rlp.EncodeToBytes(req)
	if err != nil {
		panic(err)
	}
	respmsg, err := s.disc.TalkRequest(n, "wrm", startmsg)
	if err != nil {
		return err
	}
	var resp startResponse
	if err := rlp.DecodeBytes(respmsg, &resp); err != nil {
		return fmt.Errorf("invalid response: %v", err)
	}
	if !resp.Accept {
		return fmt.Errorf("recipient rejected transfer")
	}
	return nil
}

func (s *Server) handleTalk(node enode.ID, addr *net.UDPAddr, data []byte) []byte {
	var req startRequest
	err := rlp.DecodeBytes(data, &req)
	if err != nil {
		log.Error("Invalid xfer start request", "id", node, "addr", addr, "err", err)
		return []byte{}
	}

	creq := TransferRequest{
		Node:   node,
		Addr:   addr,
		Hash:   req.Hash,
		Size:   req.Size,
		server: s,
		accept: make(chan *xferState, 1),
	}

	s.startAsRecipient <- &creq
	xfer := <-creq.accept

	var resp []byte
	if xfer != nil {
		resp, _ = rlp.EncodeToBytes(&startResponse{Accept: true})
	} else {
		resp, _ = rlp.EncodeToBytes(&startResponse{Accept: false})
	}
	return resp
}

func (s *Server) loop() {
	xfers := make(map[ID]*xferState)

	for {
		select {
		case pkt := <-s.packet:
			if len(pkt.Data) < minPacketSize {
				continue
			}
			var id ID
			copy(id[:], pkt.Data)
			xfer := xfers[id]
			if xfer != nil {
				xfer.conn.enqueue(pkt.Data[len(id):])
			} else {
				fmt.Printf("packet for unknown transfer %x\n", id[:])
			}

		case tr := <-s.startAsRecipient:
			if s.serveFunc == nil {
				tr.Reject()
				continue
			}
			id := computeID(tr.Hash, tr.Node)
			tr.xfer = s.newState(id, tr.Addr)
			tr.timeoutTimer = time.AfterFunc(500*time.Millisecond, tr.Reject)
			go func() { s.serveFunc(tr) }()

		case xfer := <-s.registerXfer:
			xfers[xfer.id] = xfer
		}
	}
}

// newState creates a new transfer state.
func (s *Server) newState(id ID, addr *net.UDPAddr) *xferState {
	conn := newKCPConn(addr, id, s.conn)
	session, err := kcp.NewConn3(0, addr, nil, ecDataShards, ecParityShards, conn)
	if err != nil {
		log.Error("Could not establish kcp session", "err", err)
		return nil
	}
	setupKCP(session)
	return &xferState{
		id:      id,
		conn:    conn,
		session: session,
	}
}

// kcpConn implements net.PacketConn for use by KCP.
type kcpConn struct {
	id     ID
	out    net.PacketConn
	buffer []byte

	mu      sync.Mutex
	flag    *sync.Cond
	inqueue [][]byte
	remote  *net.UDPAddr
}

func newKCPConn(remote *net.UDPAddr, id ID, out net.PacketConn) *kcpConn {
	o := &kcpConn{id: id, out: out, remote: remote}
	o.flag = sync.NewCond(&o.mu)
	return o
}

// enqueue adds a packet to the queue.
func (o *kcpConn) enqueue(p []byte) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.inqueue = append(o.inqueue, p)
	o.flag.Broadcast()
	// fmt.Printf("KCP enqueue n=%d\n", len(p))
}

// ReadFrom delivers a single packet from o.inqueue into the buffer p.
// If a packet does not fit into the buffer, the remaining bytes of the packet
// are discarded.
func (o *kcpConn) ReadFrom(p []byte) (n int, addr net.Addr, err error) {
	o.mu.Lock()
	for len(o.inqueue) == 0 {
		o.flag.Wait()
	}
	defer o.mu.Unlock()

	// Move packet data into p.
	n = copy(p, o.inqueue[0])

	// Delete the packet from inqueue.
	copy(o.inqueue, o.inqueue[1:])
	o.inqueue = o.inqueue[:len(o.inqueue)-1]

	// log.Info("KCP read", "buf", len(p), "n", n, "remaining-in-q", len(o.inqueue))
	// fmt.Printf("KCP read n=%d from=%v\n", n, o.remote)
	// kcpStatsDump(kcp.DefaultSnmp)
	return n, o.remote, nil
}

// WriteTo just writes to the underlying connection.
func (o *kcpConn) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	// Add id to the head of packet.
	o.buffer = o.buffer[:0]
	o.buffer = append(o.buffer, o.id[:]...)
	o.buffer = append(o.buffer, p...)

	n, err = o.out.WriteTo(o.buffer, addr)
	// fmt.Printf("KCP write n=%d to=%v\n", n, addr)
	return n, err
}

func (o *kcpConn) LocalAddr() net.Addr                { panic("not implemented") }
func (o *kcpConn) Close() error                       { return nil }
func (o *kcpConn) SetDeadline(t time.Time) error      { return nil }
func (o *kcpConn) SetReadDeadline(t time.Time) error  { return nil }
func (o *kcpConn) SetWriteDeadline(t time.Time) error { return nil }

func setupKCP(s *kcp.UDPSession) {
	s.SetMtu(1200)
	s.SetStreamMode(true)
	s.SetWriteDelay(false)
	s.SetWindowSize(256, 256)

	// https://github.com/skywind3000/kcp/blob/master/README.en.md#protocol-configuration
	// Normal Mode: ikcp_nodelay(kcp, 0, 40, 0, 0);
	// s.SetNoDelay(0, 40, 0, 0)

	// Turbo Mode: ikcp_nodelay(kcp, 1, 10, 2, 1);
	s.SetNoDelay(1, 10, 2, 1)
}
