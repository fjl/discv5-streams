package fileserver

import "github.com/fjl/discv5-streams/session"

type utpsession struct {
	s *session.Session
}

func newSession(s *session.Session) *utpsession {
	return &utpsession{s}
}

func (r *utpsession) Read(b []byte) (n int, err error) {
	return 0, nil
}

func (r *utpsession) Write(b []byte) (n int, err error) {
	return 0, nil
}

func (r *utpsession) Close() error {
	return nil
}
