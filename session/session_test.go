package session

import (
	"net"
	"net/netip"
	"testing"

	"github.com/ethereum/go-ethereum/common/mclock"
)

func TestSessionRoundtrip(t *testing.T) {
	var (
		st1 = NewStore()
		st2 = NewStore()
		ip1 = netip.MustParseAddr("127.0.0.1")
		ip2 = netip.MustParseAddr("127.0.0.2")
	)

	// Run key agreement with st1 as initiator and st2 as recipient.
	i, err := st1.Initiator("proto")
	if err != nil {
		t.Fatal(err)
	}
	i.SetHandler(dummyHandler)
	r, err := st2.Recipient("proto", ip1, i.Secret())
	if err != nil {
		t.Fatal(err)
	}
	r.SetHandler(dummyHandler)
	is := i.Establish(ip2, r.Secret())
	rs := r.Establish()
	t.Log("isession | in:", is.ingressID, "eg:", is.egressID)
	t.Log("rsession | in:", rs.ingressID, "eg:", rs.egressID)

	// Message roundtrip: initator encodes, recipient decodes.
	var encbuf []byte
	msg := []byte("test message")
	encbuf, err = is.Encode(encbuf, msg)
	if err != nil {
		t.Fatal(err)
	}

	var decbuf []byte
	decbuf, err = rs.Decode(decbuf, encbuf)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("msg2:", string(decbuf))
}

// This test checks retrieval of sessions from the store.
func TestSessionStore(t *testing.T) {
	var (
		ip1   = netip.MustParseAddr("127.0.0.1")
		ip2   = netip.MustParseAddr("127.0.0.2")
		clock = new(mclock.Simulated)
	)

	st := NewStore()
	st.clock = clock

	r, err := st.Recipient("proto", ip1, [16]byte{})
	if err != nil {
		t.Fatal(err)
	}
	r.SetHandler(dummyHandler)
	s := r.Establish()

	// Check that the session is found in the store after creating it through Recipient.
	s1 := st.Get(ip1, s.ingressID)
	if s1 == nil {
		t.Fatal("session not found")
	}

	// The session should not be found with a different IP address.
	s2 := st.Get(ip2, s.ingressID)
	if s2 != nil {
		t.Fatal("session found with wrong IP address")
	}
	// It should also not be found after it has expired.
	clock.Run(sessionTimeout)
	s3 := st.Get(ip1, s.ingressID)
	if s3 != nil {
		t.Fatal("session found after it has expired")
	}
}

func dummyHandler(s *Session, packet []byte, src net.Addr) {
	panic("handler called")
}
