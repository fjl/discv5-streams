package session

import (
	"encoding/binary"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common/mclock"
	"github.com/ethereum/go-ethereum/common/prque"
	ethlog "github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/p2p/netutil"
)

const sessionTimeout = 10 * time.Second

// Store keeps active sessions.
// This type is not safe for concurrent use.
type Store struct {
	mu       sync.Mutex
	sessions map[sessionKey]*Session
	exp      *prque.Prque[mclock.AbsTime, *Session]
	clock    mclock.Clock
}

type sessionKey struct {
	ip netip.Addr
	id uint64
}

func NewStore() *Store {
	return &Store{
		sessions: make(map[sessionKey]*Session),
		exp:      prque.New[mclock.AbsTime]((*Session).setIndex),
		clock:    mclock.System{},
	}
}

func (st *Store) store(s *Session) {
	key := sessionKey{s.ip, s.ingressID}
	st.mu.Lock()
	defer st.mu.Unlock()

	st.sessions[key] = s
	st.exp.Push(s, st.clock.Now().Add(sessionTimeout))
}

func (st *Store) HandlePacket(packet []byte, src net.Addr) bool {
	ipslice := netutil.AddrIP(src)
	if ipslice == nil {
		return false
	}
	var srcIP netip.Addr
	if ip4 := ipslice.To4(); ip4 != nil {
		srcIP, _ = netip.AddrFromSlice(ip4)
	} else {
		srcIP, _ = netip.AddrFromSlice(ipslice)
	}

	if len(packet) < 36 {
		return false
	}
	id := binary.BigEndian.Uint64(packet[:8])

	s, handler := st.get(srcIP, id)
	if s == nil {
		return false
	}
	if handler == nil {
		ethlog.Debug("Session has no handler", "ip", srcIP, "id", id)
		return false
	}
	handler(s, packet, src)
	return true
}

func (st *Store) SetSessionHandler(s *Session, handler SessionPacketHandler) {
	st.mu.Lock()
	defer st.mu.Unlock()

	s.handler = handler
}

// Get looks up a session by IP address and ID.
func (st *Store) Get(srcIP netip.Addr, id uint64) *Session {
	s, _ := st.get(srcIP, id)
	return s
}

// Get looks up a session by IP address and ID.
func (st *Store) get(srcIP netip.Addr, id uint64) (*Session, SessionPacketHandler) {
	st.mu.Lock()
	defer st.mu.Unlock()

	st.expire(st.clock.Now())
	key := sessionKey{srcIP, id}
	s, ok := st.sessions[key]
	if !ok {
		return nil, nil
	}
	st.exp.Remove(s.heapIndex)
	st.exp.Push(s, st.clock.Now().Add(sessionTimeout))
	return s, s.handler
}

// expire removes expired sessions.
func (st *Store) expire(now mclock.AbsTime) {
	for !st.exp.Empty() {
		s, exptime := st.exp.Peek()
		if exptime > now {
			break
		}
		st.exp.Pop()
		key := sessionKey{s.ip, s.ingressID}
		ethlog.Trace("Removing expired session", "ip", s.ip, "id", s.ingressID)
		delete(st.sessions, key)
	}
}
