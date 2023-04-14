package utpconn

import "sync"

// TODO: allow this to be configured per session
var sendBufferPool = sync.Pool{
	New: func() interface{} { return make([]byte, minMTU) },
}
