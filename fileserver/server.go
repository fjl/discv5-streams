package fileserver

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"time"

	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/fjl/discv5-streams/host"
)

var (
	errAlreadyAccepted = errors.New("request already accepted")
	errNotAccepted     = errors.New("request was not accepted")
)

type Config struct {
	Prefix  string // Protocol name, defaults to "xfer"
	Handler ServerFunc
}

func (cfg Config) withDefaults() Config {
	if cfg.Prefix == "" {
		cfg.Prefix = "xfer"
	}
	if cfg.Handler == nil {
		cfg.Handler = defaultHandler
	}
	return cfg
}

type ServerFunc func(*TransferRequest) error

// defaultHandler rejects all file requests.
func defaultHandler(req *TransferRequest) error {
	return nil
}

// Server is the file transfer server. It handles transfer requests from clients
// and calls the configured handler function.
type Server struct {
	cfg  *Config
	host *host.Host
}

func NewServer(host *host.Host, cfg Config) *Server {
	cfg = cfg.withDefaults()
	srv := &Server{host: host, cfg: &cfg}
	xferInit := cfg.Prefix + "-init"
	host.Discovery.RegisterTalkHandler(xferInit, srv.handleXferInit)
	return srv
}

func (s *Server) handleXferInit(node enode.ID, addr *net.UDPAddr, data []byte) []byte {
	var req xferInitRequest
	err := rlp.DecodeBytes(data, &req)
	if err != nil {
		log.Error("Invalid xferInitRequest", "id", node, "addr", addr, "err", err)
		return []byte{}
	}

	accept := make(chan bool, 1)
	creq := TransferRequest{
		Node:       node,
		Addr:       addr,
		Filename:   req.Filename,
		xferID:     req.ID,
		server:     s,
		acceptInit: accept,
	}
	go s.runHandler(&creq)

	ok := <-accept
	resp := xferInitResponse{OK: ok}
	respBytes, _ := rlp.EncodeToBytes(&resp)
	return respBytes
}

func (s *Server) runHandler(creq *TransferRequest) {
	err := s.cfg.Handler(creq)
	if err != nil {
		log.Error("File transfer handler failed", "err", err)
	}
}

func (s *Server) sendXferStart(node enode.ID, addr *net.UDPAddr, req *xferStartRequest) (*xferStartResponse, error) {
	xferStart := s.cfg.Prefix + "-start"
	reqData, _ := rlp.EncodeToBytes(req)
	respData, err := s.host.Discovery.TalkRequestToID(node, addr, xferStart, reqData)
	if err != nil {
		// Try one more time.
		time.Sleep(20 * time.Millisecond)
		respData, err = s.host.Discovery.TalkRequestToID(node, addr, xferStart, reqData)
		if err != nil {
			return nil, err
		}
	}

	var resp xferStartResponse
	if err := rlp.DecodeBytes(respData, &resp); err != nil {
		return nil, fmt.Errorf("invalid xferStartResponse: %v", err)
	}
	if !resp.OK {
		return nil, errCanceled
	}
	return &resp, nil
}

type TransferRequest struct {
	Node     enode.ID
	Addr     *net.UDPAddr
	Filename string
	xferID   uint16
	server   *Server

	acceptInit chan bool
}

func (r *TransferRequest) Accept() error {
	if r.acceptInit == nil {
		return errAlreadyAccepted
	}
	r.acceptInit <- true
	r.acceptInit = nil
	return nil
}

func (r *TransferRequest) SendFile(size uint64, reader io.Reader) error {
	if r.acceptInit != nil {
		return errNotAccepted
	}

	w, err := r.startSession(size)
	if err != nil {
		return err
	}
	defer w.Close()

	_, err = io.CopyN(w, reader, int64(size))
	return err
}

func (r *TransferRequest) startSession(fileSize uint64) (io.WriteCloser, error) {
	initiator, err := r.server.host.SessionStore.Initiator(r.server.cfg.Prefix)
	if err != nil {
		return nil, err
	}

	req := xferStartRequest{
		ID:              r.xferID,
		InitiatorSecret: initiator.Secret(),
		FileSize:        fileSize,
	}
	resp, err := r.server.sendXferStart(r.Node, r.Addr, &req)
	if err != nil {
		return nil, err
	}

	w := newSession(r.server.host.Socket)
	initiator.SetHandler(w.deliver)
	ip, _ := netip.AddrFromSlice(r.Addr.IP)
	session := initiator.Establish(ip, resp.RecipientSecret)
	w.connect(session, r.Addr)

	return w, nil
}
