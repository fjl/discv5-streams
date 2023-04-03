package session

import (
	"crypto/aes"
	"crypto/cipher"
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"time"

	"github.com/ethereum/go-ethereum/common/mclock"
	"github.com/ethereum/go-ethereum/common/prque"
	"golang.org/x/crypto/hkdf"
)

const sessionTimeout = 10 * time.Second

// Store keeps active sessions.
// This type is not safe for concurrent use.
type Store struct {
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

// Initiator is called by the session initiator to start key agreement.
// The returned initatorSec must be sent to the recipient.
func (st *Store) Initiator(protocol string) (i *InitiatorState, initiatorSec [16]byte, err error) {
	i = &InitiatorState{st: st, protocol: protocol}
	_, err = io.ReadFull(crand.Reader, i.initiatorSec[:])
	if err != nil {
		return nil, i.initiatorSec, err
	}
	return i, i.initiatorSec, nil
}

// InitiatorState is the initiator's session establishment state.
type InitiatorState struct {
	st           *Store
	protocol     string
	initiatorSec [16]byte
}

// Establish creates the session.
func (i *InitiatorState) Establish(srcIP netip.Addr, recipientSec [16]byte) *Session {
	s := &Session{ip: srcIP, heapIndex: -1}
	s.derive(i.protocol, i.initiatorSec, recipientSec, false)
	i.st.store(s)
	return s
}

// Recipient is called by the session recipient. It creates a session and returns the
// recipientSec, which must be sent back to the initiator.
func (st *Store) Recipient(srcIP netip.Addr, protocol string, initiatorSec [16]byte) (s *Session, recipientSec [16]byte, err error) {
	_, err = io.ReadFull(crand.Reader, recipientSec[:])
	if err != nil {
		return nil, recipientSec, err
	}
	s = &Session{ip: srcIP, heapIndex: -1}
	s.derive(protocol, initiatorSec, recipientSec, true)
	st.store(s)
	return s, recipientSec, nil
}

func (st *Store) store(s *Session) {
	key := sessionKey{s.ip, s.ingressID}
	st.sessions[key] = s
	st.exp.Push(s, st.clock.Now().Add(sessionTimeout))
}

// Get looks up a session by IP address and ID.
func (st *Store) Get(srcIP netip.Addr, id uint64) *Session {
	st.expire(st.clock.Now())
	key := sessionKey{srcIP, id}
	s := st.sessions[key]
	if s != nil {
		st.exp.Remove(s.heapIndex)
		st.exp.Push(s, st.clock.Now().Add(sessionTimeout))
	}
	return s
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
		delete(st.sessions, key)
	}
}

// Encryption/authentication parameters.
const (
	aesKeySize   = 16
	gcmNonceSize = 12
)

// Session represents an active session.
type Session struct {
	ip           netip.Addr
	ingressID    uint64
	ingressKey   [16]byte
	egressID     uint64
	egressKey    [16]byte
	nonceCounter uint32

	heapIndex int
}

func (s *Session) setIndex(i int) {
	s.heapIndex = i
}

// derive creates the session keys.
func (s *Session) derive(protocol string, initiatorSec, recipientSec [16]byte, isRecipient bool) {
	var sec [32]byte
	defer func() {
		for i := range sec {
			sec[i] = 0
		}
	}()
	copy(sec[:16], initiatorSec[:])
	copy(sec[16:], recipientSec[:])

	info := "discv5 subprotocol session" + protocol
	kdf := hkdf.New(sha256.New, sec[:], nil, []byte(info))
	var kdata [48]byte
	kdf.Read(kdata[:])

	copy(s.ingressKey[:], kdata[0:16])
	copy(s.egressKey[:], kdata[16:32])
	s.ingressID = binary.BigEndian.Uint64(kdata[32:40])
	s.egressID = binary.BigEndian.Uint64(kdata[40:48])

	if isRecipient {
		s.ingressKey, s.egressKey = s.egressKey, s.ingressKey
		s.ingressID, s.egressID = s.egressID, s.ingressID
	}
}

// Encode creates an encrypted packet containing msg.
// dest must not overlap with msg.
func (s *Session) Encode(dest []byte, msg []byte) ([]byte, error) {
	nonceValue := s.nonceCounter
	s.nonceCounter++
	var nonceData [gcmNonceSize]byte
	binary.BigEndian.PutUint32(nonceData[:], nonceValue)
	if _, err := crand.Read(nonceData[4:]); err != nil {
		return nil, errors.New("can't generate nonce")
	}

	var idData [8]byte
	binary.BigEndian.PutUint64(idData[:], s.egressID)

	dest = append(dest, idData[:]...)
	dest = append(dest, nonceData[:]...)
	dest = s.encrypt(dest, msg, nonceData[:], idData[:])
	return dest, nil
}

// Decode decrypts/authenticates a packet and appends the plaintext to dest.
// dest must not overlap with packet.
func (s *Session) Decode(dest []byte, packet []byte) ([]byte, error) {
	if len(packet) < 36 {
		return nil, errors.New("packet too short")
	}

	idData := packet[:8]
	nonceData := packet[8:20]
	return s.decrypt(dest, packet[20:], nonceData, idData)
}

// encrypt encrypts msg with the session's egress key. The ciphertext is appended to dest,
// which must not overlap with plaintext. A fresh nonce is generated and returned.
func (s *Session) encrypt(dest []byte, plaintext, nonce, authData []byte) []byte {
	block, err := aes.NewCipher(s.egressKey[:])
	if err != nil {
		panic(fmt.Errorf("can't create block cipher: %v", err))
	}
	aesgcm, err := cipher.NewGCMWithNonceSize(block, gcmNonceSize)
	if err != nil {
		panic(fmt.Errorf("can't create GCM: %v", err))
	}
	return aesgcm.Seal(dest, nonce, plaintext, authData)
}

// decrypt decrypts/authenticates a ciphertext with the session's ingress key and the
// given nonce. The plaintext is appended to dest, which must not overlap with ciphertext.
func (s *Session) decrypt(dest, ciphertext, nonce, authData []byte) ([]byte, error) {
	block, err := aes.NewCipher(s.ingressKey[:])
	if err != nil {
		return nil, fmt.Errorf("can't create block cipher: %v", err)
	}
	if len(nonce) != gcmNonceSize {
		return nil, fmt.Errorf("invalid GCM nonce size: %d", len(nonce))
	}
	aesgcm, err := cipher.NewGCMWithNonceSize(block, gcmNonceSize)
	if err != nil {
		return nil, fmt.Errorf("can't create GCM: %v", err)
	}
	return aesgcm.Open(dest, nonce, ciphertext, authData)
}