package fileserver

import (
	"bytes"
	"context"
	"io"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fjl/discv5-streams/host"
)

// func init() {
// 	log.Root().SetHandler(log.LvlFilterHandler(log.LvlTrace, log.StreamHandler(os.Stderr, log.TerminalFormat(false))))
// }

func TestTransfer(t *testing.T) {
	host1, _ := host.Listen("127.0.0.1:0", host.Config{})
	defer host1.Close()

	host2, _ := host.Listen("127.0.0.1:0", host.Config{})
	defer host2.Close()

	serverConfig := Config{Handler: ServeFS(testFS)}
	NewServer(host1, serverConfig)

	client := NewClient(host2, Config{})
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, err := client.Request(ctx, host1.Discovery.Self(), "file")
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

var testContent []byte
var testFS = fstest.MapFS{}

func init() {
	testContent = make([]byte, 100000)
	for i := range testContent {
		testContent[i] = byte(i)
	}
	testFS["file"] = &fstest.MapFile{Data: testContent}
}
