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

// Config is the configuration of Host.
type Config struct {
	ListenAddr string
	NodeDB     string // Path to node database directory.
	Discovery  discover.Config
}

var ConfigForTesting = Config{
	ListenAddr: "127.0.0.1:0",
	Discovery: discover.Config{
		Bootnodes: []*enode.Node{}, // disable bootstrap
	},
}

// Host manages the p2p networking stack.
type Host struct {
	Socket       *sharedsocket.Conn
	LocalNode    *enode.LocalNode
	Discovery    *discover.UDPv5
	SessionStore *session.Store
}

// Listen creates a UDP listener on the configured address, and sets up the p2p
// networking stack.
func Listen(cfg Config) (*Host, error) {
	// Assign config defaults.
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ":0"
	}
	if cfg.Discovery.PrivateKey == nil {
		ethlog.Info("Generating new node key")
		key, err := crypto.GenerateKey()
		if err != nil {
			return nil, err
		}
		cfg.Discovery.PrivateKey = key
	}
	if cfg.Discovery.Bootnodes == nil {
		cfg.Discovery.Bootnodes = parseDefaultBootnodes()
	}

	// Listen.
	conn, err := sharedsocket.Listen("udp4", cfg.ListenAddr)
	if err != nil {
		return nil, err
	}

	// Configure LocalNode.
	db, _ := enode.OpenDB(cfg.NodeDB)
	ln := enode.NewLocalNode(db, cfg.Discovery.PrivateKey)
	laddr := conn.LocalAddr().(*net.UDPAddr)
	if laddr.IP.IsUnspecified() {
		ln.SetFallbackIP(net.IPv4(127, 0, 0, 1))
	} else {
		ln.SetFallbackIP(laddr.IP)
	}
	ln.SetFallbackUDP(laddr.Port)

	// Configure discovery.
	discoverConn := conn.DefaultConn()
	disc, err := discover.ListenV5(discoverConn, ln, cfg.Discovery)
	if err != nil {
		conn.Close()
		return nil, err
	}

	// Configure session system.
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
