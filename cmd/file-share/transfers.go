package main

import (
	"context"
	"encoding/gob"
	"io"
	"log"
	"math"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fjl/discv5-streams/fileserver"
)

// transfersController manages file transfers.
type transfersController struct {
	stateFile string
	net       *networkController
	state     atomic.Pointer[transfersState]
	changeCh  chan struct{}

	wg           sync.WaitGroup
	startCh      chan fileserver.TransferRef
	updateCh     chan *transfer
	retryLoadCh  chan struct{}
	resetStateCh chan struct{}
	clientCh     chan *fileserver.Client
	closeCh      chan struct{}
	rootContext  context.Context
	rootCancel   context.CancelFunc
}

type transfersState struct {
	wasReset  bool // this is for internal use by mainLoop
	loading   bool
	loadError error
	list      []*transfer
}

func (s *transfersState) maxID() uint64 {
	var id uint64
	for _, tx := range s.list {
		if tx.ID > id {
			id = tx.ID
		}
	}
	return id
}

//go:generate go run golang.org/x/tools/cmd/stringer@latest -type transferStatus
type transferStatus uint

const (
	transferStatusCreated transferStatus = iota
	transferStatusResolving
	transferStatusConnecting
	transferStatusDownloading
	transferStatusDone
	transferStatusError
)

type transfer struct {
	ref fileserver.TransferRef

	ID        uint64
	Name      string
	Status    transferStatus
	Created   time.Time
	Size      int64  // total file size (as announced by server)
	ReadBytes int64  // bytes downloaded so far
	ReadSpeed int64  // bytes per second
	DestFile  string // destination/output file
	Error     string
}

func newTransfersController(net *networkController, stateFile string) *transfersController {
	t := &transfersController{
		stateFile:    stateFile,
		net:          net,
		changeCh:     make(chan struct{}, 1),
		clientCh:     make(chan *fileserver.Client),
		startCh:      make(chan fileserver.TransferRef),
		updateCh:     make(chan *transfer),
		retryLoadCh:  make(chan struct{}),
		resetStateCh: make(chan struct{}),
		closeCh:      make(chan struct{}),
	}
	net.SetClientChan(t.clientCh)
	t.rootContext, t.rootCancel = context.WithCancel(context.Background())
	t.publishState(transfersState{loading: true})

	t.wg.Add(1)
	go t.start()
	return t
}

// Close terminates the transfer system.
func (t *transfersController) Close() {
	close(t.closeCh)
	t.wg.Wait()
}

// Changed returns a channel that fires whenever the file list
// has changed. This is used to trigger UI updates.
func (t *transfersController) Changed() <-chan struct{} {
	return t.changeCh
}

// State returns the current state.
func (t *transfersController) State() *transfersState {
	return t.state.Load()
}

// RetryLoad requests that the state should be reloaded from disk.
func (t *transfersController) RetryLoad() {
	select {
	case t.retryLoadCh <- struct{}{}:
	case <-t.closeCh:
	}
}

// ResetState requests that the state should be emptied.
func (t *transfersController) ResetState() {
	select {
	case t.resetStateCh <- struct{}{}:
	case <-t.closeCh:
	}
}

// StartTransfer starts a file download.
func (t *transfersController) StartTransfer(ref fileserver.TransferRef) {
	select {
	case t.startCh <- ref:
	case <-t.closeCh:
	}
}

func (t *transfersController) start() {
	defer t.wg.Done()

	// Load the state file.
	var state transfersState
retryLoad:
	state.list, state.loadError = t.loadList()
	if state.loadError != nil {
		// There was an error loading the state file.
		// Wait for a retry signal from the UI.
		t.publishState(state)
		select {
		case <-t.retryLoadCh:
			goto retryLoad
		case <-t.resetStateCh:
			state.reset()
		case <-t.closeCh:
			return
		}
	}
	log.Printf("transfers: state loaded")
	t.publishState(state)

	// Wait for the network to come up.
	var client *fileserver.Client
waitForNetwork:
	for {
		select {
		case client = <-t.clientCh:
			break waitForNetwork
		case <-t.resetStateCh:
			state.reset()
		case <-t.retryLoadCh:
			// Retry can be ignored because the file was successfully loaded.
		case <-t.closeCh:
			return
		}
	}

	t.mainLoop(state, client)
}

// mainLoop handles transfer requests and manages the transfer list.
func (t *transfersController) mainLoop(state transfersState, client *fileserver.Client) {
	var (
		saveRequested = state.wasReset
		idCounter     = state.maxID()
		saveDone      chan struct{}
	)
	for {
		// Launch save if requested and not already running.
		if saveRequested && saveDone == nil {
			saveDone = make(chan struct{})
			saveRequested = false
			go func() {
				err := t.saveList(state.list)
				if err != nil {
					log.Println("transfers: save error:", err)
				}
				saveDone <- struct{}{}
			}()
		}

		select {
		case tr := <-t.startCh:
			tx := t.startTransfer(idCounter, client, tr)
			idCounter++
			state.add(tx)
			t.publishState(state)

		case tx := <-t.updateCh:
			state.update(tx)
			t.publishState(state)
			switch tx.Status {
			case transferStatusDone, transferStatusError:
				saveRequested = true
			}

		case <-saveDone:
			state.wasReset = false
			saveDone = nil
			log.Printf("transfers: state saved")

		case client = <-t.clientCh:

		case <-t.retryLoadCh:
			// Ignore late retry requests.

		case <-t.resetStateCh:
			state.reset()
			saveRequested = true

		case <-t.closeCh:
			if saveDone != nil {
				<-saveDone
			} else {
				t.saveList(state.list)
			}
			return
		}
	}
}

func (state *transfersState) reset() {
	state.list = nil
	state.wasReset = true
	state.loadError = nil
}

func (state *transfersState) add(tx transfer) {
	list := make([]*transfer, len(state.list)+1)
	copy(list, state.list)
	list[len(list)-1] = &tx
	state.list = list
}

func (state *transfersState) update(tx *transfer) {
	list := make([]*transfer, len(state.list))
	for i, item := range state.list {
		if item.ID == tx.ID {
			list[i] = tx
		} else {
			list[i] = item
		}
	}
	state.list = list
}

// publishState updates the current state and triggers a UI refresh.
func (t *transfersController) publishState(s transfersState) {
	t.state.Store(&s)
	select {
	case t.changeCh <- struct{}{}:
	default:
	}
}

func (t *transfersController) loadList() ([]*transfer, error) {
	fd, err := os.Open(t.stateFile)
	if err != nil {
		log.Println(err)
		if os.IsNotExist(err) {
			err = nil
		}
		return nil, err
	}

	var list []*transfer
	dec := gob.NewDecoder(fd)
	err = dec.Decode(&list)
	fd.Close()

	if err != nil {
		log.Printf("transfers: load error: %v", err)
		return nil, err
	}

	return list, nil
}

func (t *transfersController) saveList(list []*transfer) error {
	// Remove in-progress transfers from list.
	savedList := make([]*transfer, 0, len(list))
	for _, tx := range list {
		if tx.Status == transferStatusDone || tx.Status == transferStatusError {
			savedList = append(savedList, tx)
		}
	}

	fd, err := os.OpenFile(t.stateFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer fd.Close()

	enc := gob.NewEncoder(fd)
	return enc.Encode(savedList)
}

func (t *transfersController) startTransfer(id uint64, client *fileserver.Client, ref fileserver.TransferRef) transfer {
	tx := transfer{
		ID:     id,
		ref:    ref,
		Name:   ref.File,
		Status: transferStatusConnecting,
	}
	go t.download(client, tx)
	return tx
}

// updateTransfer sends a transfer update to the main loop.
func (t *transfersController) updateTransfer(tx transfer) {
	select {
	case t.updateCh <- &tx:
	case <-t.rootContext.Done():
	}
}

// download executes a file transfer.
func (t *transfersController) download(client *fileserver.Client, tx transfer) {
	r, err := client.Request(t.rootContext, tx.ref.Node, tx.ref.File)
	if err != nil {
		tx.Status = transferStatusError
		tx.Error = err.Error()
		t.updateTransfer(tx)
		return
	}
	defer r.Close()

	tx.Status = transferStatusDownloading
	tx.Size = r.Size()
	t.updateTransfer(tx)

	pr := newProgressReader(r, func(bytes int64, speed int64) {
		tx.ReadBytes = bytes
		tx.ReadSpeed = speed
		t.updateTransfer(tx)
	})

	n, err := io.CopyN(io.Discard, pr, tx.Size)
	if err != nil {
		tx.Status = transferStatusError
		tx.Error = err.Error()
	} else {
		tx.Status = transferStatusDone
	}

	pr.close()

	tx.ReadBytes = n
	t.updateTransfer(tx)
}

// progressReader wraps an io.Reader and reports progress.
type progressReader struct {
	src    io.Reader
	report progressFunc
	bytes  atomic.Int64
	closed chan struct{}
	wg     sync.WaitGroup
}

type progressFunc func(bytes int64, speed int64)

func newProgressReader(src io.Reader, report progressFunc) *progressReader {
	r := &progressReader{
		src:    src,
		report: report,
		closed: make(chan struct{}, 1),
	}
	r.wg.Add(1)
	go r.reportLoop()
	return r
}

func (r *progressReader) Read(p []byte) (int, error) {
	n, err := r.src.Read(p)
	r.bytes.Add(int64(n))
	return n, err
}

// close stops the progress reporting loop.
func (r *progressReader) close() {
	close(r.closed)
	r.wg.Wait()
}

// reportLoop periodically invokes the progress reporting function.
func (r *progressReader) reportLoop() {
	defer r.wg.Done()

	var (
		ticker    = time.NewTicker(100 * time.Millisecond)
		lastRead  = time.Now()
		lastBytes = r.bytes.Load()
		sma       = newSMA(10)
	)
	for {
		select {
		case <-ticker.C:
			now := time.Now()
			bytes := r.bytes.Load()
			diff := bytes - lastBytes
			sma.sample(float64(diff) / now.Sub(lastRead).Seconds())
			lastRead, lastBytes = now, bytes
			r.report(bytes, int64(math.Round(sma.value())))
		case <-r.closed:
			return
		}
	}
}

// sma implements a simple moving average.
type sma struct {
	samples []float64
	i       int
}

func newSMA(nsamples int) *sma {
	return &sma{
		samples: make([]float64, 0, nsamples),
	}
}

// sample adds a new sample.
func (s *sma) sample(v float64) {
	if len(s.samples) < cap(s.samples) {
		s.samples = append(s.samples, v)
	} else {
		s.samples[s.i] = v
		s.i = (s.i + 1) % len(s.samples)
	}
}

// value returns the average of the collected samples.
func (s *sma) value() float64 {
	var sum float64
	for i := range s.samples {
		sum += s.samples[i]
	}
	return sum / float64(len(s.samples))
}
