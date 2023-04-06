package fileserver

// TALK messages.
type (
	xferInitRequest struct {
		ID       uint16
		Filename string
	}

	xferInitResponse struct {
		OK bool
	}

	xferStartRequest struct {
		ID              uint16
		InitiatorSecret [16]byte
		FileSize        uint64
	}

	xferStartResponse struct {
		OK              bool
		RecipientSecret [16]byte
	}
)
