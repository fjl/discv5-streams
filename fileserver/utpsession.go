package fileserver

import (
	"net"

	ethlog "github.com/ethereum/go-ethereum/log"
	"github.com/fjl/discv5-streams/host"
	"github.com/fjl/discv5-streams/session"
	"github.com/fjl/discv5-streams/sharedsocket"
	"github.com/fjl/discv5-streams/utpconn"
)

type utpsession struct {
	socket  *sharedsocket.Conn
	conn    *utpconn.Conn
	session *session.Session

	decBuffer []byte
	encBuffer []byte
}

func newSession(host *host.Host, s *session.Session, remote net.Addr) *utpsession {
	us := &utpsession{
		socket:    host.Socket,
		session:   s,
		decBuffer: make([]byte, 2048),
		encBuffer: make([]byte, 2048),
	}
	host.SessionStore.SetSessionHandler(s, us.deliver)
	us.conn = utpconn.NewConn(us.socket.LocalAddr(), remote, us.packetOut)
	return us
}

func (r *utpsession) deliver(s *session.Session, packet []byte, src net.Addr) {
	data, err := s.Decode(r.decBuffer[:0], packet)
	if err != nil {
		return
	}
	ethlog.Trace("Received uTP packet", "size", len(data))
	r.conn.PacketIn(data)
}

func (r *utpsession) packetOut(b []byte, dst net.Addr) (n int, err error) {
	ethlog.Trace("Sending uTP packet", "size", len(b), "dst", dst)
	data, err := r.session.Encode(r.encBuffer[:0], b)
	if err != nil {
		return 0, err
	}
	if _, err = r.socket.WriteTo(data, dst); err != nil {
		return 0, err
	}
	return len(b), nil
}

func (r *utpsession) Read(b []byte) (n int, err error) {
	return r.conn.Read(b)
}

func (r *utpsession) Write(b []byte) (n int, err error) {
	return r.conn.Write(b)
}

func (r *utpsession) Close() error {
	return r.conn.Close()
}
