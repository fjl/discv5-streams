package fileserver

import (
	"fmt"
	"io/fs"
	"path"
)

// ServeFS serves transfer requests from the given file system.
func ServeFS(fsys fs.FS) ServerFunc {
	return func(tr *TransferRequest) error {
		return serveFile(fsys, tr)
	}
}

func serveFile(fsys fs.FS, tr *TransferRequest) error {
	filename := path.Clean(tr.Filename)
	if filename == "." || !fs.ValidPath(filename) {
		return fs.ErrInvalid
	}

	f, err := fsys.Open(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	if err = tr.Accept(); err != nil {
		return err
	}

	stat, err := f.Stat()
	if err != nil {
		return err
	}
	err = tr.SendFile(uint64(stat.Size()), f)
	if err != nil {
		err = fmt.Errorf("send error: %w", err)
	}
	return err
}
