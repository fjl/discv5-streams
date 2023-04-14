package host

import (
	"net"

	"github.com/ethereum/go-ethereum/crypto"
	ethlog "github.com/ethereum/go-ethereum/log"
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
		ethlog.Info("Generating new node key")
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

	// Configure LocalNode.
	db, _ := enode.OpenDB("")
	ln := enode.NewLocalNode(db, cfg.Discovery.PrivateKey)
	laddr := conn.LocalAddr().(*net.UDPAddr)
	if laddr.IP.IsUnspecified() {
		ln.SetFallbackIP(net.IPv4(127, 0, 0, 1))
	} else {
		ln.SetFallbackIP(laddr.IP)
	}
	ln.SetFallbackUDP(laddr.Port)

	discoverConn := conn.DefaultConn()
	disc, err := discover.ListenV5(discoverConn, ln, cfg.Discovery)
	if err != nil {
		conn.Close()
		return nil, err
	}

	sessionStore := session.NewStore()
	conn.AddHandler(sessionStore)

	stack := &Host{
		Socket:       conn,
		LocalNode:    ln,
		Discovery:    disc,
		SessionStore: sessionStore,
	}
	return stack, nil
}

// Close terminates the stack.
func (s *Host) Close() error {
	s.Discovery.Close()
	return s.Socket.Close()
}
