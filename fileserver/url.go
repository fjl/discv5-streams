package fileserver

import (
	"errors"
	"net/url"
	"strings"

	"github.com/ethereum/go-ethereum/p2p/enode"
)

// TransferRef is a reference to a file on a remote node.
type TransferRef struct {
	Node *enode.Node
	File string
}

// ParseURL parses a transfer reference URL.
func ParseURL(text string) (ref TransferRef, err error) {
	u, err := url.Parse(text)
	if err != nil {
		return ref, errors.New("invalid URL")
	}
	if u.Scheme != "discv5fs" {
		return ref, errors.New("missing/wrong URL scheme")
	}
	enr := "enr:" + u.Host
	node, err := enode.Parse(enode.ValidSchemes, enr)
	if err != nil {
		return ref, errors.New("invalid ENR host")
	}
	if u.Path == "" || u.Path == "/" {
		return ref, errors.New("empty file path")
	}
	file := strings.TrimPrefix(u.Path, "/")
	return TransferRef{Node: node, File: file}, nil
}

// String encodes the transfer reference as a URL.
func (ref *TransferRef) String() string {
	u := url.URL{
		Scheme: "discv5fs",
		Host:   strings.TrimPrefix(ref.Node.String(), "enr:"),
		Path:   ref.File,
	}
	return u.String()
}
