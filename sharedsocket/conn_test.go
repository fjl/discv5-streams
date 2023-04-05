package sharedsocket

import (
	"errors"
	"fmt"
	"net"
	"reflect"
	"testing"
	"time"
)

func TestConnDispatch(t *testing.T) {
	c1, err := Listen("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer c1.Close()

	c2, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()

	var h1called = make(chan bool, 1)
	handler1 := HandlerFunc(func(b []byte, from net.Addr) bool {
		match := string(b) == "h1"
		h1called <- match
		return match
	})
	c1.AddHandler(handler1)

	// send a packet
	_, err = c2.WriteTo([]byte("h1"), c1.LocalAddr())
	if err != nil {
		t.Fatal(err)
	}
	// wait for the handler to receive it
	timeout := 1 * time.Second
	if err := tryRecv(h1called, true, timeout); err != nil {
		t.Fatal("handler:", err)
	}
}

// This test checks the DefaultConn functionality.
func TestDefaultConn(t *testing.T) {
	c1, err := Listen("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer c1.Close()

	c2, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()

	var h1called = make(chan bool, 1)
	handler1 := HandlerFunc(func(b []byte, from net.Addr) bool {
		match := string(b) == "h1"
		h1called <- match
		return match
	})
	c1.AddHandler(handler1)

	var (
		dcread = make(chan readEvent, 1)
		dc     = c1.DefaultConn()
	)
	go func() {
		msg := make([]byte, 1024)
		n, addr, err := dc.ReadFrom(msg)
		p := readEvent{msg[:n], addr, err}
		dcread <- p
	}()

	// send a packet
	_, err = c2.WriteTo([]byte("h1"), c1.LocalAddr())
	if err != nil {
		t.Fatal(err)
	}
	// wait for the handler to receive it
	timeout := 1 * time.Second
	if err := tryRecv(h1called, true, timeout); err != nil {
		t.Fatal("handler:", err)
	}

	// send a non-matching packet
	_, err = c2.WriteTo([]byte("other"), c1.LocalAddr())
	if err != nil {
		t.Fatal(err)
	}
	// wait for the handler to receive it
	if err := tryRecv(h1called, false, timeout); err != nil {
		t.Fatal("handler:", err)
	}
	// wait for the default conn to receive it
	expectedEv := readEvent{[]byte("other"), c2.LocalAddr(), nil}
	if err := tryRecv(dcread, expectedEv, timeout); err != nil {
		t.Fatal("default conn:", err)
	}
}

// This test checks that closing the DefaultConn removes it, and a new one can be
// created right away.
func TestDefaultConnClose(t *testing.T) {
	c1, err := Listen("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer c1.Close()

	c2, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()

	dc := c1.DefaultConn()
	dc.Close()

	// send a non-matching packet
	_, err = c2.WriteTo([]byte("other"), c1.LocalAddr())
	if err != nil {
		t.Fatal(err)
	}
}

type readEvent struct {
	data []byte
	from net.Addr
	err  error
}

func tryRecv[T any](ch <-chan T, expected T, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case v := <-ch:
		if !reflect.DeepEqual(v, expected) {
			return fmt.Errorf("%v does not match expected value %v", v, expected)
		}
		return nil
	case <-timer.C:
		return errors.New("receive timeout")
	}
}
