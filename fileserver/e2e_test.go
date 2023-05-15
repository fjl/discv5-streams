package fileserver

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"testing/fstest"
	"time"

	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/fjl/discv5-streams/host"
)

// func init() {
//	log.Root().SetHandler(log.LvlFilterHandler(log.LvlTrace, log.StreamHandler(os.Stderr, log.TerminalFormat(false))))
// }

var testContent []byte
var testFS = fstest.MapFS{}

func init() {
	testContent = make([]byte, 100000)
	for i := range testContent {
		testContent[i] = byte(i)
	}
	testFS["file"] = &fstest.MapFile{Data: testContent}
}

type testSetup struct {
	serverHost *host.Host
	clientHost *host.Host
	server     *Server
	client     *Client
}

func newTestSetup(t *testing.T) *testSetup {
	host1, err := host.Listen(host.ConfigForTesting)
	if err != nil {
		t.Fatal("listen error:", err)
	}

	host2, err := host.Listen(host.ConfigForTesting)
	if err != nil {
		host1.Close()
		t.Fatal("listen error:", err)
	}

	serverConfig := Config{Handler: ServeFS(testFS)}
	return &testSetup{
		serverHost: host1,
		clientHost: host2,
		server:     NewServer(host1, serverConfig),
		client:     NewClient(host2, Config{}),
	}
}

func (s *testSetup) close() {
	s.serverHost.Close()
	s.clientHost.Close()
}

func (s *testSetup) serverNode() *enode.Node {
	return s.serverHost.Discovery.Self()
}

func TestTransfer(t *testing.T) {
	test := newTestSetup(t)
	defer test.close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, err := test.client.Request(ctx, test.serverNode(), "file")
	if err != nil {
		t.Fatal("request error:", err)
	}
	content, err := io.ReadAll(r)
	if err != nil {
		t.Fatal("read error:", err)
	}
	if !bytes.Equal(content, testContent) {
		t.Fatal("wrong file content")
	}
}

func TestClientTransferSize(t *testing.T) {
	test := newTestSetup(t)
	defer test.close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, err := test.client.Request(ctx, test.serverNode(), "file")
	if err != nil {
		t.Fatal("request error:", err)
	}

	if r.Size() != int64(len(testContent)) {
		t.Fatal("wrong size")
	}
	r.Close()
}

func TestClientRejectHandling(t *testing.T) {
	test := newTestSetup(t)
	defer test.close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// The server should reject the transfer below, because it has an invalid file name.
	_, err := test.client.Request(ctx, test.serverNode(), "///")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestClientTimeoutHandling(t *testing.T) {
	test := newTestSetup(t)
	defer test.close()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// The server will just time out this request, because the file does not exist.
	_, err := test.client.Request(ctx, test.serverNode(), "wrong-file")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("expected timeout error")
	}
}
