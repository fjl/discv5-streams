package main

import (
	"encoding/gob"
	"errors"
	"io/fs"
	"log"
	"os"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/fjl/discv5-streams/fileserver"
)

// filesController keeps the files that can be downloaded by peers.
type filesController struct {
	files         atomic.Pointer[filesState]
	changeEventCh chan struct{}

	stateFile    string
	wg           sync.WaitGroup
	addCh        chan *fileRef
	removeCh     chan *fileRef
	retryLoadCh  chan struct{}
	resetStateCh chan struct{}
	closeCh      chan struct{}
}

type filesState struct {
	loading   bool // true during initialization
	loadError error
	list      fileList
}

type fileList []*fileRef

type fileRef struct {
	Name string
	Path string

	info fs.FileInfo
}

// add returns a copy of l with ref added.
func (l fileList) add(ref *fileRef) fileList {
	// Skip if already in list.
	for _, f := range l {
		if f.Path == ref.Path {
			return l
		}
	}

	var newlist fileList
	newlist = append(newlist, l...)
	newlist = append(newlist, ref)
	sort.Slice(newlist, func(i, j int) bool {
		return newlist[i].Name < newlist[j].Name
	})
	return newlist
}

// remove returns a copy of l with ref removed.
func (l fileList) remove(ref *fileRef) fileList {
	var newlist fileList
	for _, f := range l {
		if f != ref {
			newlist = append(newlist, f)
		}
	}
	return newlist
}

func newFilesController(stateFile string) *filesController {
	fs := &filesController{
		stateFile:     stateFile,
		changeEventCh: make(chan struct{}, 1),
		addCh:         make(chan *fileRef),
		removeCh:      make(chan *fileRef),
		retryLoadCh:   make(chan struct{}),
		resetStateCh:  make(chan struct{}),
		closeCh:       make(chan struct{}),
	}
	fs.publish(true, nil, nil)
	fs.wg.Add(1)
	go fs.stateLoop()
	return fs
}

// Files returns the current file list.
func (fc *filesController) Files() *filesState {
	return fc.files.Load()
}

// ServeFile serves a file to a peer.
func (fc *filesController) ServeFile(tr *fileserver.TransferRequest) error {
	state := fc.Files()
	if state.loadError != nil {
		return state.loadError
	}
	if state.loading {
		return errors.New("file list is loading")
	}
	for _, f := range state.list {
		if f.Name == tr.Filename {
			return fc.serveFile(tr, f)
		}
	}
	return &fs.PathError{
		Op:   "open",
		Path: tr.Filename,
		Err:  fs.ErrNotExist,
	}
}

func (fc *filesController) serveFile(tr *fileserver.TransferRequest, f *fileRef) error {
	if err := tr.Accept(); err != nil {
		return err
	}
	r, err := os.Open(f.Path)
	if err != nil {
		return err
	}
	defer r.Close()

	info, err := r.Stat()
	if err != nil {
		return err
	}
	return tr.SendFile(uint64(info.Size()), r)
}

// Changed returns a channel that fires whenever the file list
// has changed. This is used to trigger UI updates.
func (fc *filesController) Changed() <-chan struct{} {
	return fc.changeEventCh
}

// AddFile adds a file to the file space.
func (fc *filesController) AddFile(path string, info fs.FileInfo) {
	fr := &fileRef{info.Name(), path, info}
	select {
	case fc.addCh <- fr:
	case <-fc.closeCh:
	}
}

// RemoveFile removes a file from the file space.
func (fc *filesController) RemoveFile(fr *fileRef) {
	select {
	case fc.removeCh <- fr:
	case <-fc.closeCh:
	}
}

// RetryLoad forces a reload of the file list from disk.
func (fc *filesController) RetryLoad() {
	select {
	case fc.retryLoadCh <- struct{}{}:
	case <-fc.closeCh:
	}
}

// RemoveDatabase removes the database file and starts over with an empty file list.
func (fc *filesController) ResetDatabase() {
	select {
	case fc.resetStateCh <- struct{}{}:
	case <-fc.closeCh:
	}
}

// Close closes the file space.
func (fc *filesController) Close() {
	close(fc.closeCh)
	fc.wg.Wait()
}

// stateLoop maintains the file list.
func (fc *filesController) stateLoop() {
	defer fc.wg.Done()

	var (
		state         fileList
		saveDone      chan struct{}
		saveRequested bool
		err           error
	)

	// Load the initial state from the file.
	for {
		state, err = fc.loadState()
		if err == nil {
			fc.publish(false, state, nil)
			goto runMainLoop
		}

		// There was an error loading the state file.
		// Wait for a retry signal from the UI.
		fc.publish(false, nil, err)
		select {
		case <-fc.retryLoadCh:
			// Retry.
		case <-fc.resetStateCh:
			state = nil
			saveRequested = true
			goto runMainLoop
		case <-fc.closeCh:
			return
		}
	}

runMainLoop:
	log.Println("fileSpace: state loaded")
	fc.publish(false, state, nil)
	for {
		// Launch save if requested and not already running.
		if saveRequested && saveDone == nil {
			saveDone = make(chan struct{})
			saveRequested = false
			go func() {
				err := fc.saveState(state)
				if err != nil {
					log.Println("fileSpace: save error:", err)
				}
				saveDone <- struct{}{}
			}()
		}

		select {
		case ref := <-fc.addCh:
			state = state.add(ref)
			saveRequested = true
			fc.publish(false, state, nil)

		case ref := <-fc.removeCh:
			state = state.remove(ref)
			saveRequested = true
			fc.publish(false, state, nil)

		case <-saveDone:
			saveDone = nil

		case <-fc.retryLoadCh:
			// Ignore load error requests.

		case <-fc.resetStateCh:
			state = fileList{}
			saveRequested = true

		case <-fc.closeCh:
			if saveDone != nil {
				<-saveDone
			} else {
				fc.saveState(state)
			}
			return
		}
	}
}

// loadState loads the file list from the state file.
func (fc *filesController) loadState() ([]*fileRef, error) {
	fd, err := os.Open(fc.stateFile)
	if err != nil {
		log.Println(err)
		if os.IsNotExist(err) {
			err = nil
		}
		return nil, err
	}

	var files []*fileRef
	dec := gob.NewDecoder(fd)
	err = dec.Decode(&files)
	fd.Close()

	if err != nil {
		log.Printf("fileSpace: load error: %v", err)
		return nil, err
	}

	// Update file sizes, and check if any files have gone missing.
	for i := 0; i < len(files); i++ {
		f := files[i]
		f.info, err = os.Stat(f.Path)
		if err != nil {
			log.Printf("fileSpace: removing stale file: %s", err)
			files = append(files[:i], files[i+1:]...)
		}
	}
	return files, nil
}

// saveState saves the file list to the state file.
func (fc *filesController) saveState(list fileList) error {
	fd, err := os.OpenFile(fc.stateFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer fd.Close()

	enc := gob.NewEncoder(fd)
	return enc.Encode(list)
}

func (fc *filesController) publish(loading bool, list fileList, err error) {
	fc.files.Store(&filesState{loading, err, list})
	select {
	case fc.changeEventCh <- struct{}{}:
	default:
	}
}
