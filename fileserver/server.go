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

type ServerFunc func(*FileTransferRequest) error

// defaultHandler rejects all file requests.
func defaultHandler(req *FileTransferRequest) error {
	return nil
}

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
	creq := FileTransferRequest{
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

func (s *Server) runHandler(creq *FileTransferRequest) {
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

type FileTransferRequest struct {
	Node     enode.ID
	Addr     *net.UDPAddr
	Filename string
	xferID   uint16
	server   *Server

	acceptInit chan bool
}

func (r *FileTransferRequest) Accept() error {
	if r.acceptInit == nil {
		return errAlreadyAccepted
	}
	r.acceptInit <- true
	r.acceptInit = nil
	return nil
}

func (r *FileTransferRequest) SendFile(size uint64, reader io.Reader) error {
	if r.acceptInit != nil {
		return errNotAccepted
	}

	w, err := r.startSession(size)
	if err != nil {
		return err
	}

	_, err = io.CopyN(w, reader, int64(size))
	return err
}

func (r *FileTransferRequest) startSession(fileSize uint64) (io.Writer, error) {
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

	ip, _ := netip.AddrFromSlice(r.Addr.IP)
	session := initiator.Establish(ip, resp.RecipientSecret)
	w := newSession(r.server.host, session, r.Addr)
	return w, nil
}
