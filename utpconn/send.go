package utpconn

import (
	"context"
	"time"

	"github.com/anacrolix/missinggo"
	"github.com/brendoncarroll/stdctx/logctx"
)

type send struct {
	acked       missinggo.Event
	payloadSize uint32
	started     time.Time
	_type       st
	connID      uint16
	payload     []byte
	seqNr       uint16
	conn        *Conn

	acksSkipped int
	resendTimer *time.Timer
	numResends  int
}

// first is true if this is the first time the send is acked. latency is
// calculated for the first ack.
func (s *send) Ack() (latency time.Duration, first bool) {
	first = !s.acked.IsSet()
	if first {
		latency = time.Since(s.started)
	}
	if s.payload != nil {
		sendBufferPool.Put(s.payload[:0:minMTU])
		s.payload = nil
	}
	s.acked.Set()
	if s.resendTimer != nil {
		s.resendTimer.Stop()
		s.resendTimer = nil
	}
	return
}

func (s *send) timedOut() {
	s.conn.destroy(ErrTimeout{IsAck: true})
}

func (s *send) timeoutResend() {
	s.conn.mu.Lock()
	defer s.conn.mu.Unlock()

	if time.Since(s.started) >= s.conn.config.writeTimeout {
		s.timedOut()
		return
	}
	if s.acked.IsSet() || s.conn.destroyed.IsSet() {
		return
	}
	rt := s.conn.resendTimeout()
	s.resend()
	s.numResends++
	s.resendTimer.Reset(rt * time.Duration(s.numResends))
}

func (s *send) resend() {
	if s.acked.IsSet() {
		return
	}
	err := s.conn.send(s._type, s.connID, s.payload, s.seqNr)
	if err != nil {
		logctx.Warnf(context.TODO(), "error resending packet: %s", err)
	}
}
