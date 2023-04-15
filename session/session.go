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
	"net"
	"net/netip"

	"golang.org/x/crypto/hkdf"
)

// Initiator is called by the session initiator to start key agreement.
// The initiator secret of the returned state should be sent to the recipient.
func (st *Store) Initiator(protocol string) (i *InitiatorState, err error) {
	i = &InitiatorState{st: st, protocol: protocol}
	_, err = io.ReadFull(crand.Reader, i.secret[:])
	if err != nil {
		return nil, err
	}
	return i, nil
}

// InitiatorState is the initiator's session establishment state.
type InitiatorState struct {
	st       *Store
	protocol string
	secret   [16]byte
	handler  SessionPacketHandler
}

// Secret returns the initiator secret.
func (is *InitiatorState) Secret() [16]byte {
	return is.secret
}

// SetHandler sets the packet handler for the session.
// This must be called before Establish.
func (is *InitiatorState) SetHandler(h SessionPacketHandler) {
	is.handler = h
}

// Establish creates the session.
func (is *InitiatorState) Establish(srcIP netip.Addr, recipientSecret [16]byte) *Session {
	if is.handler == nil {
		panic("no handler set")
	}
	s := &Session{ip: srcIP, heapIndex: -1, handler: is.handler}
	s.derive(is.protocol, &is.secret, &recipientSecret, false)
	is.st.store(s)
	for i := range is.secret {
		is.secret[i] = 0
	}
	return s
}

// Recipient is called by the session recipient. The recipient secret of the returned
// state should be sent to the initiator.
func (st *Store) Recipient(protocol string, srcIP netip.Addr, initiatorSecret [16]byte) (*RecipientState, error) {
	r := &RecipientState{
		st: st,
		s:  &Session{ip: srcIP, heapIndex: -1},
	}
	_, err := io.ReadFull(crand.Reader, r.secret[:])
	if err != nil {
		return nil, err
	}
	r.s.derive(protocol, &initiatorSecret, &r.secret, true)
	return r, nil
}

// RecipientState is the recipient's session establishment state.
type RecipientState struct {
	st     *Store
	s      *Session
	secret [16]byte
}

// Secret returns the recipient secret.
func (r *RecipientState) Secret() [16]byte {
	return r.secret
}

// SetHandler sets the packet handler for the session.
// This must be called before Establish.
func (r *RecipientState) SetHandler(h SessionPacketHandler) {
	r.s.handler = h
}

// Establish creates the session.
func (r *RecipientState) Establish() *Session {
	if r.s.handler == nil {
		panic("no handler set")
	}
	for i := range r.secret {
		r.secret[i] = 0
	}
	r.st.store(r.s)
	return r.s
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
	handler      SessionPacketHandler

	heapIndex int
}

type SessionPacketHandler func(*Session, []byte, net.Addr)

func (s *Session) setIndex(i int) {
	s.heapIndex = i
}

// derive creates the session keys.
func (s *Session) derive(protocol string, initiatorSec, recipientSec *[16]byte, isRecipient bool) {
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
// which must not overlap with plaintext.
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
