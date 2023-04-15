package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/ethereum/go-ethereum/crypto"
	ethlog "github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/fjl/discv5-streams/fileserver"
	"github.com/fjl/discv5-streams/host"
)

func main() {
	var (
		// server mode:
		serveFlag = flag.String("serve", "", "serve files (directory)")
		// client:
		dlFlag   = flag.String("file", "", "download file")
		nodeFlag = flag.String("node", "", "node to connect to")
		// common flags:
		listenAddr = flag.String("laddr", ":0", "UDP listen address")
		keyFile    = flag.String("nodekey", "", "node key file")
	)
	flag.Parse()

	h := ethlog.LvlFilterHandler(ethlog.LvlTrace, ethlog.StreamHandler(os.Stderr, ethlog.TerminalFormat(true)))
	ethlog.Root().SetHandler(h)

	// Load node key, if requested. Otherwise a new key will be generated
	// by the host.
	var hostconfig host.Config
	if *keyFile != "" {
		key, err := crypto.LoadECDSA(*keyFile)
		if err != nil {
			log.Fatal("can't load key file:", err)
			return
		}
		hostconfig.Discovery.PrivateKey = key
	}

	// Create the host.
	host, err := host.Listen(*listenAddr, hostconfig)
	if err != nil {
		log.Fatalf("can't listen: %v", err)
		return
	}
	defer host.Close()

	// If server mode is requested, run as server.
	var config fileserver.Config
	if *serveFlag != "" {
		dir := *serveFlag
		dirinfo, err := os.Stat(dir)
		if err != nil {
			log.Fatalf("can't open -serve directory: %v", err)
		}
		if !dirinfo.IsDir() {
			log.Fatalf("-serve path is not a directory")
		}
		fmt.Println("server ENR:", host.LocalNode.Node().String())
		config.Handler = fileserver.ServeFS(os.DirFS(dir))
		fileserver.NewServer(host, config)
		select {}
	}

	// Run as client.
	if *dlFlag == "" {
		log.Fatalf("no file to download")
		return
	}
	node, err := enode.Parse(enode.ValidSchemes, *nodeFlag)
	if err != nil {
		log.Fatalf("invalid node: %v", err)
		return
	}

	ctx := context.Background()
	client := fileserver.NewClient(host, config)
	defer client.Close()

	r, err := client.Request(ctx, node, *dlFlag)
	if err != nil {
		log.Fatalf("request error: %v", err)
		return
	}
	fmt.Println("copying file to stdout")
	io.Copy(os.Stdout, r)
	fmt.Println("done")
}
