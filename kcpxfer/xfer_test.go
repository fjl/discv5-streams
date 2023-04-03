package kcpxfer

import (
	"bytes"
	"crypto/sha256"
	"io"
	"net"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/p2p/discover"
	"github.com/ethereum/go-ethereum/p2p/enode"
)

func listenV5(t *testing.T, bootnodes []*enode.Node, unhandled chan discover.ReadPacket) (*discover.UDPv5, *net.UDPConn) {
	key, _ := crypto.GenerateKey()
	db, _ := enode.OpenDB("")
	ln := enode.NewLocalNode(db, key)

	socket, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("can't listen: %v", err)
	}
	t.Cleanup(func() { socket.Close() })

	// Configure UDP endpoint in ENR from listener address.
	usocket := socket.(*net.UDPConn)
	uaddr := socket.LocalAddr().(*net.UDPAddr)
	ln.SetStaticIP(uaddr.IP)
	ln.SetFallbackUDP(uaddr.Port)

	cfg := discover.Config{
		PrivateKey: key,
		Bootnodes:  bootnodes,
		Unhandled:  unhandled,
	}
	disc, err := discover.ListenV5(usocket, ln, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { disc.Close() })

	return disc, usocket
}

func TestXfer(t *testing.T) {
	unhandled1 := make(chan discover.ReadPacket, 100)
	disc1, s1 := listenV5(t, nil, unhandled1)

	unhandled2 := make(chan discover.ReadPacket, 100)
	disc2, s2 := listenV5(t, []*enode.Node{disc1.Self()}, unhandled2)

	var (
		content     = make([]byte, 1024*1024)
		contentHash = sha256.Sum256(content)
	)

	cfg1 := ServerConfig{Discovery: disc1, Conn: s1, InChannel: unhandled1}
	server1 := NewServer(cfg1)

	var done = make(chan struct{})
	cfg2 := ServerConfig{
		Discovery: disc2,
		Conn:      s2,
		InChannel: unhandled2,
		Handler: func(tr *TransferRequest) error {
			conn, err := tr.Accept()
			if err != nil {
				t.Error("accept error:", err)
				return err
			}
			t.Logf("accepted transfer hash=%x size=%d", tr.Hash[:], tr.Size)

			data, _ := io.ReadAll(io.LimitReader(conn, int64(tr.Size)))
			if !bytes.Equal(data, content) {
				t.Error("content mismatch")
			}
			t.Log("received", len(data), "bytes")
			close(done)
			return nil
		},
	}
	NewServer(cfg2)

	session, err := server1.Transfer(disc2.Self(), contentHash, int64(len(content)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(session, bytes.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	t.Log("sent", len(content), "bytes")

	<-done
}
