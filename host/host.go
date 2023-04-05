package host

import (
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/p2p/discover"
	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/fjl/discv5-streams/session"
	"github.com/fjl/discv5-streams/sharedsocket"
)

type Config struct {
	Discovery discover.Config
}

type Host struct {
	Socket       *sharedsocket.Conn
	LocalNode    *enode.LocalNode
	Discovery    *discover.UDPv5
	SessionStore *session.Store
}

func Listen(addr string, cfg Config) (*Host, error) {
	if cfg.Discovery.PrivateKey == nil {
		key, err := crypto.GenerateKey()
		if err != nil {
			return nil, err
		}
		cfg.Discovery.PrivateKey = key
	}

	conn, err := sharedsocket.Listen("udp4", addr)
	if err != nil {
		return nil, err
	}

	db, _ := enode.OpenDB("")
	ln := enode.NewLocalNode(db, cfg.Discovery.PrivateKey)
	discoverConn := conn.DefaultConn()
	disc, err := discover.ListenV5(discoverConn, ln, cfg.Discovery)
	if err != nil {
		conn.Close()
		return nil, err
	}

	stack := &Host{
		Socket:       conn,
		LocalNode:    ln,
		Discovery:    disc,
		SessionStore: session.NewStore(),
	}
	return stack, nil
}

// Close terminates the stack.
func (s *Host) Close() error {
	s.Discovery.Close()
	return s.Socket.Close()
}
