package utpconn

import (
	"encoding/hex"
	"testing"

	"github.com/davecgh/go-spew/spew"
	"github.com/stretchr/testify/assert"
)

func TestSelectiveAckBitmaskBytesLen(t *testing.T) {
	for _, _case := range []struct {
		BitIndex    int
		ExpectedLen int
	}{
		{0, 4},
		{31, 4},
		{32, 8},
	} {
		var selAck selectiveAckBitmask
		selAck.SetBit(_case.BitIndex)
		assert.EqualValues(t, _case.ExpectedLen, len(selAck.Bytes))
	}
}

func TestDecode(t *testing.T) {
	b, _ := hex.DecodeString("210000000023a2240000041f00100000f1fc0001")
	var hdr header
	_, err := hdr.Unmarshal(b)
	if err != nil {
		t.Fatal(err)
	}
	spew.Dump(hdr)
}
