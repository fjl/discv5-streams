package utpconn

import (
	"context"
	"time"
)

const (
	DefaultBacklogLen = 50
)

// SocketOption configures a Socket
type SocketOption func(*socketConfig)

type socketConfig struct {
	bgCtx context.Context

	writeTimeout      time.Duration
	initialLatency    time.Duration
	packetReadTimeout time.Duration

	backlogLen int

	connConfig
}

func defaultSocketConfig() socketConfig {
	return socketConfig{
		bgCtx: context.Background(),

		writeTimeout:      15 * time.Second,
		initialLatency:    400 * time.Millisecond,
		packetReadTimeout: 2 * time.Minute,

		backlogLen: DefaultBacklogLen,
	}
}

// WithBackground sets the background context.
// This can be used to enable instrumentation with the stdctx library.
func WithBackground(ctx context.Context) SocketOption {
	return func(c *socketConfig) {
		c.bgCtx = ctx
	}
}

// If a write isn't acked within this period, destroy the connection.
func WithWriteTimeout(d time.Duration) SocketOption {
	return func(c *socketConfig) {
		c.writeTimeout = d
	}
}

// This is the latency we assume on new connections. It should be higher
// than the latency we expect on most connections to prevent excessive
// resending to peers that take a long time to respond, before we've got a
// better idea of their actual latency.
func WithInitialLatency(d time.Duration) SocketOption {
	return func(c *socketConfig) {
		c.initialLatency = d
	}
}

func WithPacketReadTimeout(d time.Duration) SocketOption {
	return func(c *socketConfig) {
		c.packetReadTimeout = d
	}
}

// Maximum received SYNs that haven't been accepted. If more SYNs are
// received, a pseudo randomly selected SYN is replied to with a reset to
// make room.
func WithBacklogLen(n int) SocketOption {
	return func(c *socketConfig) {
		c.backlogLen = n
	}
}

func WithConnOption(o ConnOption) SocketOption {
	return func(c *socketConfig) {
		o(&c.connConfig)
	}
}

type connConfig struct{}

// ConnOption is used to configure Conn specific options
type ConnOption func(*connConfig)
