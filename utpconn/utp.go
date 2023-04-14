package utpconn

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/brendoncarroll/stdctx/units"
)

const (
	// IPv6 min MTU is 1280, -40 for IPv6 header, and ~8 for fragment header?
	minMTU = 1438 // Why?

	// uTP header of 20, +2 for the next extension, and an optional selective
	// ACK.
	maxHeaderSize  = 20 + 2 + (((maxUnackedInbound+7)/8)+3)/4*4
	maxPayloadSize = minMTU - maxHeaderSize
	maxRecvSize    = 0x2000

	// Maximum out-of-order packets to buffer.
	maxUnackedInbound = 256
	maxUnackedSends   = 256

	readBufferLen = 1 << 20 // ~1MiB

	// How long to wait before sending a state packet, after one is required.
	// This prevents spamming a state packet for every packet received, and
	// non-state packets that are being sent also fill the role.
	pendingSendStateDelay = 500 * time.Microsecond
)

type read struct {
	data []byte
	from net.Addr
}

type syn struct {
	seq_nr, conn_id uint16
	addr            net.Addr
}

type st int

func (me st) String() string {
	switch me {
	case stData:
		return "stData"
	case stFin:
		return "stFin"
	case stState:
		return "stState"
	case stReset:
		return "stReset"
	case stSyn:
		return "stSyn"
	default:
		panic(fmt.Sprintf("%d", me))
	}
}

const (
	stData  st = 0
	stFin   st = 1
	stState st = 2
	stReset st = 3
	stSyn   st = 4

	// Used for validating packet headers.
	stMax = stSyn
)

type recv struct {
	seen bool
	data []byte
	Type st
}

func nowTimestamp() uint32 {
	return uint32(time.Now().UnixNano() / int64(time.Microsecond))
}

func seqLess(a, b uint16) bool {
	if b < 0x8000 {
		return a < b || a >= b-0x8000
	} else {
		return a < b && a >= b-0x8000
	}
}

func telemIncr(ctx context.Context, m string, x any, u units.Unit) {

}

func telemMark(ctx context.Context, m string, x any, u units.Unit) {

}
