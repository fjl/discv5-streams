package main

import (
	"crypto/ecdsa"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/fjl/discv5-streams/fileserver"
	"github.com/fjl/discv5-streams/host"
)

// networkController is the networkController connection state.
type networkController struct {
	datadir     string
	hostConfig  *host.Config
	serveFunc   fileserver.ServerFunc
	state       atomic.Pointer[networkState]
	changeCh    chan struct{}
	setClientCh chan chan<- *fileserver.Client

	wg        sync.WaitGroup
	closeCh   chan struct{}
	restartCh chan struct{}
}

type networkState struct {
	loading    bool
	startError error
	stats      networkStats
}

type networkStats struct {
	TableNodes int
	LocalENR   *enode.Node
}

func (net *networkController) Close() {
	close(net.closeCh)
	net.wg.Wait()
}

// State returns the current network state.
func (net *networkController) State() *networkState {
	return net.state.Load()
}

// Restart restarts the network.
func (net *networkController) Restart() {
	select {
	case net.restartCh <- struct{}{}:
	case <-net.closeCh:
	}
}

// SetClientChan sets the channel on which client instances are published.
// This is used by transferController to get the client.
func (net *networkController) SetClientChan(ch chan<- *fileserver.Client) {
	select {
	case net.setClientCh <- ch:
	case <-net.closeCh:
	}
}

// Changed returns a notificationchannel that fires when network
// state has changed.This is used to update the UI.
func (net *networkController) Changed() <-chan struct{} {
	return net.changeCh
}

func newNetworkController(dataDirectory string, config *host.Config, serve fileserver.ServerFunc) *networkController {
	net := &networkController{
		datadir:     dataDirectory,
		hostConfig:  config,
		serveFunc:   serve,
		changeCh:    make(chan struct{}, 1),
		setClientCh: make(chan chan<- *fileserver.Client),
		restartCh:   make(chan struct{}),
		closeCh:     make(chan struct{}),
	}
	net.publishState(&networkState{loading: true})
	net.wg.Add(1)
	go net.loop()
	return net
}

func (net *networkController) loop() {
	defer net.wg.Done()

restart:
	host, client, err := net.start()
	if err != nil {
		net.publishState(&networkState{startError: err})
		select {
		case <-net.restartCh:
			goto restart
		case <-net.closeCh:
			return
		}
	}
	defer host.Close()

	log.Printf("network: node ID %v", host.LocalNode.ID())
	tick := time.NewTicker(1 * time.Second)
	defer tick.Stop()

	var (
		clientCh chan<- *fileserver.Client
	)
	for {
		select {
		case ch := <-net.setClientCh:
			clientCh = ch
			clientCh <- client

		case <-tick.C:
			net.update(host)

		case <-net.restartCh:
			host.Close()
			goto restart

		case <-net.closeCh:
			return
		}
	}
}

func (net *networkController) start() (*host.Host, *fileserver.Client, error) {
	cfg := *net.hostConfig

	// Load node key, if requested. Otherwise, generate a new one and
	// store it for next time.
	if cfg.Discovery.PrivateKey == nil {
		key, err := net.getNodeKey()
		if err != nil {
			return nil, nil, err
		}
		cfg.Discovery.PrivateKey = key
	}

	// Create the host.
	host, err := host.Listen(cfg)
	if err != nil {
		log.Printf("can't listen: %v", err)
		return nil, nil, err
	}

	// Register the file server protocol.
	config := fileserver.Config{Handler: net.serveFunc}
	client := fileserver.NewClient(host, config)
	fileserver.NewServer(host, config)

	return host, client, nil
}

func (net *networkController) getNodeKey() (*ecdsa.PrivateKey, error) {
	err := os.MkdirAll(net.datadir, 0700)
	if err != nil {
		return nil, err
	}

	keyFile := filepath.Join(net.datadir, "nodekey")
	key, err := crypto.LoadECDSA(keyFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			key, err = crypto.GenerateKey()
			if err != nil {
				log.Printf("network: can't generate key: %v", err)
				return nil, err
			}
			if err := crypto.SaveECDSA(keyFile, key); err != nil {
				log.Printf("network: can't save key: %v", err)
				return nil, err
			}
		}
	}
	return key, nil
}

func (net *networkController) update(host *host.Host) {
	stats := networkStats{
		TableNodes: len(host.Discovery.AllNodes()),
		LocalENR:   host.LocalNode.Node(),
	}
	net.publishState(&networkState{stats: stats})
}

func (net *networkController) publishState(state *networkState) {
	net.state.Store(state)
	select {
	case net.changeCh <- struct{}{}:
	default:
	}
}
