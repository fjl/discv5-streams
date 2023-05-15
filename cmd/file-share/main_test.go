package main

import (
	"testing"
	"time"

	"github.com/fjl/discv5-streams/host"
)

func TestAppStateSetup(t *testing.T) {
	tmp := t.TempDir()
	state := newAppState(tmp, host.ConfigForTesting)
	defer state.Close()

	var (
		start       = time.Now()
		networkUp   bool
		transfersUp bool
		filesUp     bool
	)

	// Wait for network.
	for !networkUp && time.Since(start) < 2*time.Second {
		s := state.net.State()
		switch {
		case s.loading:
			<-state.net.Changed()
			continue
		case s.startError != nil:
			t.Fatal("Network start error:", s.startError)
		default:
			networkUp = true
		}
	}

	// Wait for transfers.
	for !transfersUp && time.Since(start) < 2*time.Second {
		s := state.transfers.State()
		switch {
		case s.loading:
			<-state.transfers.Changed()
			continue
		case s.loadError != nil:
			t.Fatal("Transfers load error:", s.loadError)
		default:
			transfersUp = true
		}
	}

	// Wait for files.
	for !filesUp && time.Since(start) < 2*time.Second {
		s := state.fs.State()
		switch {
		case s.loading:
			<-state.fs.Changed()
			continue
		case s.loadError != nil:
			t.Fatal("Files load error:", s.loadError)
		default:
			filesUp = true
		}
	}

	if !networkUp {
		t.Fatal("networkController did not start")
	}
	if !transfersUp {
		t.Fatal("transfersController did not start")
	}
	if !filesUp {
		t.Fatal("filesController did not start")
	}
}
