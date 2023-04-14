package utpconn

import (
	"errors"
	"net"
)

var (
	ErrClosed = net.ErrClosed
)

type ErrTimeout struct {
	IsAck bool
	Msg   string
}

func (e ErrTimeout) Timeout() bool   { return true }
func (e ErrTimeout) Error() string   { return "utp: timeout. " + e.Msg }
func (e ErrTimeout) Temporary() bool { return false }

func IsTimeout(err error) bool {
	return errors.As(err, &ErrTimeout{})
}

func IsAckTimeout(err error) bool {
	var e ErrTimeout
	return errors.As(err, &e) && e.IsAck
}
