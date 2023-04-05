package sharedsocket

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Handler is a packet handler.
type Handler interface {
	HandlePacket(packet []byte, addr net.Addr) bool
}

type handlerFunc struct {
	f func(packet []byte, addr net.Addr) bool
}

func (h *handlerFunc) HandlePacket(packet []byte, addr net.Addr) bool {
	return h.f(packet, addr)
}

// HandlerFunc creates a handler that calls f.
func HandlerFunc(f func(packet []byte, addr net.Addr) bool) Handler {
	return &handlerFunc{f}
}

type UDPConn interface {
	net.PacketConn
	ReadFromUDP(b []byte) (n int, addr *net.UDPAddr, err error)
	WriteToUDP(b []byte, addr *net.UDPAddr) (n int, err error)
}

// Conn is a UDP listener that allows multiple applications to share the same port.
//
// Applications can register one ore more handler functions. Incoming packets are
// dispatched to all handlers. If the packet is accepted by a handler, it is not processed
// further.
//
// Conn can be used to send outgoing packets, i.e. it implements the writing side of
// net.PacketConn.
//
// In order to integrate with code that uses a net.PacketConn directly, a 'default outlet'
// can be retrieved using the DefaultConn method. The returned connection object is a
// net.PacketConn that receives all packets that weren't accepted by any handler.
type Conn struct {
	conn UDPConn

	wg       sync.WaitGroup
	quit     chan struct{}
	mutex    sync.Mutex // protects writes to the handler list
	handlers atomic.Pointer[handlerList]
}

// NewConn creates a new connection.
func NewConn(p UDPConn) *Conn {
	c := &Conn{
		conn: p,
		quit: make(chan struct{}),
	}
	c.handlers.Store(new(handlerList))
	c.wg.Add(1)
	go c.readLoop()
	return c
}

// Listen creates a UDP listener and wraps it with a Conn.
func Listen(network, address string) (*Conn, error) {
	pc, err := net.ListenPacket(network, address)
	if err != nil {
		return nil, err
	}
	udpc, ok := pc.(UDPConn)
	if !ok {
		pc.Close()
		return nil, fmt.Errorf("ListenPacket returned a non-UDP connection (type %T)", pc)
	}
	return NewConn(udpc), nil
}

// Close terminates the connection.
// This also closes the underlying connection.
func (c *Conn) Close() error {
	// If there is a defaultConn, it needs to be closed as well. But defaultConn.Close()
	// would acquire c.mutex in unsetDefaultConn, causing a deadlock.
	// So it is closed by the defer construction below.
	var dcToClose *defaultConn
	defer func() {
		if dcToClose != nil {
			dcToClose.Close()
		}
	}()

	c.mutex.Lock()
	defer c.mutex.Unlock()

	if c.quit == nil {
		return nil
	}

	l := c.handlers.Load()
	if l.defaultConn != nil {
		dcToClose = l.defaultConn
	}
	close(c.quit)
	err := c.conn.Close()
	c.wg.Wait()
	c.quit = nil
	return err
}

// WriteTo writes a packet with payload b to addr. This is a direct write
// to the underlying connection.
func (c *Conn) WriteTo(b []byte, addr net.Addr) (int, error) {
	return c.conn.WriteTo(b, addr)
}

// WriteTo writes a packet with payload b to addr. This is a direct write
// to the underlying connection.
func (c *Conn) WriteToUDP(b []byte, addr *net.UDPAddr) (int, error) {
	return c.conn.WriteToUDP(b, addr)
}

// LocalAddr returns the local network address of the socket, if known.
func (c *Conn) LocalAddr() net.Addr {
	return c.conn.LocalAddr()
}

// AddHandler defines a new handler for incoming packets.
// The order in which handlers are added matters.Handlers will be called in the
// order they were added.
func (c *Conn) AddHandler(h Handler) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	l := c.handlers.Load()
	c.handlers.Store(l.append(h))
}

// RemoveHandler removes a handler.
func (c *Conn) RemoveHandler(h Handler) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	l := c.handlers.Load()
	c.handlers.Store(l.remove(h))
}

// DefaultConn creates and retrieves the default outlet. This connection receives all
// packets that are not accepted by any handler. Note that only one default outlet exists
// at any time. The first call to DefaultConn creates it, and subsequent calls return the
// existing connection. The default outlet connection is removed when it is closed.
func (c *Conn) DefaultConn() UDPConn {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	l := c.handlers.Load()
	if l.defaultConn == nil {
		dc := newDefaultConn(c)
		c.handlers.Store(l.setDefault(dc))
		return dc
	}
	return l.defaultConn
}

func (c *Conn) unsetDefaultConn() {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	l := c.handlers.Load()
	c.handlers.Store(l.setDefault(nil))
}

func (c *Conn) readLoop() {
	defer c.wg.Done()

	var (
		buf = make([]byte, 2048)
	)
recv:
	for {
		n, addr, err := c.conn.ReadFromUDP(buf)
		if errors.Is(err, net.ErrClosed) {
			return
		} else if err != nil {
			// Nothing can be done about the errors here. To avoid
			// a busy loop, it's best to sleep for little bit before continuing.
			log.Printf("read error: %v", err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		packet := buf[:n]

		l := c.handlers.Load()
		for _, h := range l.hs {
			if h.HandlePacket(packet, addr) {
				continue recv
			}
		}
		if l.defaultConn != nil {
			l.defaultConn.deliver(packet, addr, c.quit)
		}
	}
}

// handlerList keeps the list of packet handlers and the optional default outlet.
// This is implemented as a copy-on-write structure because the handlers
type handlerList struct {
	hs          []Handler
	defaultConn *defaultConn
}

func (l *handlerList) append(h Handler) *handlerList {
	newlist := make([]Handler, 0, len(l.hs)+1)
	newlist = append(newlist, l.hs...)
	newlist = append(newlist, h)
	return &handlerList{newlist, l.defaultConn}
}

func (l *handlerList) remove(h Handler) *handlerList {
	for i := range l.hs {
		if l.hs[i] == h {
			return l.removeIndex(i)
		}
	}
	return l
}

func (l *handlerList) removeIndex(i int) *handlerList {
	newlist := make([]Handler, 0, len(l.hs)-1)
	newlist = append(newlist, l.hs[:i]...)
	newlist = append(newlist, l.hs[i+1:]...)
	return &handlerList{newlist, l.defaultConn}
}

func (l *handlerList) setDefault(dc *defaultConn) *handlerList {
	return &handlerList{l.hs, dc}
}

// defaultConn is a net.PacketConn that relays unmatched incoming packets on Conn.
type defaultConn struct {
	conn         *Conn
	in           chan *packet
	readDeadline *time.Timer
	mutex        sync.Mutex
	buffers      []*packet
	closed       bool
}

type packet struct {
	b    []byte
	addr *net.UDPAddr
}

func newDefaultConn(c *Conn) *defaultConn {
	return &defaultConn{
		conn: c,
		in:   make(chan *packet, 100),
	}
}

// deliver delivers a packet to the application.
func (dc *defaultConn) deliver(b []byte, addr *net.UDPAddr, quit chan struct{}) bool {
	p, ok := dc.getPacket(b, addr)
	if !ok {
		return false // connection closed
	}
	select {
	case dc.in <- p:
	case <-quit:
		dc.recyclePacket(p)
	}
	return true
}

// LocalAddr returns the local network address, if known.
func (dc *defaultConn) LocalAddr() net.Addr {
	return dc.conn.LocalAddr()
}

// Close closes the connection.
// Note this also removes dc as the default outlet from the Conn.
func (dc *defaultConn) Close() error {
	dc.mutex.Lock()
	defer dc.mutex.Unlock()

	if !dc.closed {
		close(dc.in)
		dc.closed = true
		dc.conn.unsetDefaultConn()
	}
	return nil
}

// ReadFrom reads a packet from the connection.
func (dc *defaultConn) ReadFrom(b []byte) (int, net.Addr, error) {
	n, addr, err := dc.ReadFromUDP(b)
	return n, addr, err
}

// ReadFromUDP reads a packet from the connection.
func (dc *defaultConn) ReadFromUDP(b []byte) (int, *net.UDPAddr, error) {
	var timeout <-chan time.Time
	if dc.readDeadline != nil {
		timeout = dc.readDeadline.C
	}

	select {
	case p, ok := <-dc.in:
		if !ok {
			return 0, nil, io.EOF
		}
		n := copy(b, p.b)
		dc.recyclePacket(p)
		return n, p.addr, nil
	case <-timeout:
		dc.readDeadline = nil
		// TODO: return net.OpError with timeout == true
		return 0, nil, errors.New("timeout")
	}
}

// SetReadDeadline sets the deadline of the next read.
func (dc *defaultConn) SetReadDeadline(t time.Time) error {
	if dc.readDeadline == nil {
		dc.readDeadline = time.NewTimer(time.Now().Sub(t))
	} else {
		dc.readDeadline.Reset(t.Sub(time.Now()))
	}
	return nil
}

// WriteTo writes a packet to the connection.
func (dc *defaultConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	return dc.conn.WriteTo(b, addr)
}

// WriteTo writes a packet to the connection.
func (dc *defaultConn) WriteToUDP(b []byte, addr *net.UDPAddr) (int, error) {
	return dc.conn.WriteTo(b, addr)
}

// SetWriteDeadline sets the deadline of the next write. Note that this affects the
// underlying connection directly, i.e. other users of the SharedConn will be affected as
// well.
func (dc *defaultConn) SetWriteDeadline(t time.Time) error {
	return dc.conn.conn.SetWriteDeadline(t)
}

// SetDeadline sets the read and write deadline.
func (dc *defaultConn) SetDeadline(t time.Time) error {
	dc.SetReadDeadline(t)
	return dc.SetWriteDeadline(t)
}

// getPacket retrieves a packet from the buffer pool.
func (dc *defaultConn) getPacket(b []byte, addr *net.UDPAddr) (*packet, bool) {
	dc.mutex.Lock()
	defer dc.mutex.Unlock()

	if dc.closed {
		return nil, false
	}
	if len(dc.buffers) == 0 {
		return &packet{b, addr}, true
	}
	p := dc.buffers[len(dc.buffers)-1]
	dc.buffers = dc.buffers[:len(dc.buffers)-1]
	p.b = append(p.b[:0], b...)
	p.addr = addr
	return p, true
}

// recyclePacket returns a packet to the buffer pool.
func (dc *defaultConn) recyclePacket(p *packet) {
	dc.mutex.Lock()
	defer dc.mutex.Unlock()

	dc.buffers = append(dc.buffers, p)
}
