package dcr_sdk

import (
	"bytes"
	myg "github.com/mygaru/dcr-sdk/mygaru-client"
	"github.com/stretchr/testify/assert"
	"strings"
	"testing"
	"time"
)

func TestSDK_Scan(t *testing.T) {
	mygClient := myg.Init(241, 30*time.Second)
	segmentId := uint32(1000002)

	uids := []string{"acefwevreger9", "acefwevreger1", "acefwevreger0", "d", "e", "f", "j", "h", "i", "h2", "4"}
	inter1, err := mygClient.Scan(uids, segmentId)
	assert.Nil(t, err)

	uidsBytes := []byte(strings.Join(uids, ",\n"))
	inter2, err := mygClient.ScanReader(bytes.NewBuffer(uidsBytes), segmentId)

	inter3, err := mygClient.ScanBytes(uidsBytes, segmentId)

	assert.True(t, inter1 == inter2 && inter3 == inter1)
}

func TestSDK_Check_ExternalUID(t *testing.T) {
	mygClient := myg.Init(241, 30*time.Second)

	ok, err := mygClient.Check("acefwevreger9", 1000002, myg.IdentifierTypeExternal)
	assert.Nil(t, err)

	t.Log(ok)
}
