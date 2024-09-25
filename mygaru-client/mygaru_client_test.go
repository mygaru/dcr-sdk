package mygaru_client

import (
	"bytes"
	"fmt"
	"github.com/stretchr/testify/assert"
	"strings"
	"testing"
	"time"
)

func TestSDK_Scan(t *testing.T) {
	mygClient := Init(241, 30*time.Second)
	segmentId := uint32(1000002)

	uids := make([]string, 100)
	for i := 0; i < 100; i++ {
		uids[i] = fmt.Sprintf("acefwevreger%d", i)
	}
	inter1, err := mygClient.Scan(uids, segmentId)
	assert.Nil(t, err)

	uidsBytes := []byte(strings.Join(uids, ",\n"))
	inter2, err := mygClient.ScanReader(bytes.NewBuffer(uidsBytes), segmentId)
	assert.Nil(t, err)

	inter3, err := mygClient.ScanBytes(uidsBytes, segmentId)
	assert.Nil(t, err)

	assert.True(t, inter1 == inter2 && inter3 == inter1)
}

func TestSDK_Check_ExternalUID(t *testing.T) {
	mygClient := Init(241, 30*time.Second)

	ok, err := mygClient.Check("acefwevreger9", 1000002, IdentifierTypeExternal)
	assert.Nil(t, err)

	t.Log(ok)
}
