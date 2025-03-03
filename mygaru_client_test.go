package dcr_sdk

import (
	"bytes"
	"fmt"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"math/rand"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSDK_Scan(t *testing.T) {
	mygClient := Init(241, 30*time.Second, 5*time.Second, 300)
	segmentId := uint32(1000002)

	puids := make([]string, 100)
	for i := 0; i < 100; i++ {
		puids[i] = fmt.Sprintf("acefwevreger%d", i)
	}

	inter1, err := mygClient.Scan(puids, segmentId)
	assert.Nil(t, err)

	puidsBytes := []byte(strings.Join(puids, ",\n"))
	inter2, err := mygClient.ScanReader(bytes.NewBuffer(puidsBytes), segmentId)
	assert.Nil(t, err)

	inter3, err := mygClient.ScanBytes(puidsBytes, segmentId)
	assert.Nil(t, err)

	assert.True(t, inter1 == inter2 && inter3 == inter1)
}

func TestSDK_Check_Multiple(t *testing.T) {
	mygClient := Init(6, 30*time.Second, 500*time.Millisecond, 50)
	n := 10
	sameUID := 10
	segmentIDs := make([]uint32, n)
	uids := make([]string, n)
	wg := sync.WaitGroup{}
	wg.Add(n)

	for i := 0; i < n; i++ {
		segmentIDs[i] = rand.Uint32()
		if i%sameUID == 0 {
			uids[i] = uuid.NewString()
		} else {
			uids[i] = uids[i-1]
		}

		go func(i int) {
			ok, err := mygClient.Check(uids[i], segmentIDs[i], IdentifierTypeDeviceID)
			if err != nil {
				t.Error(err)
				wg.Done()
				return
			}
			if err == nil {
				fmt.Printf("uid: %s, seg: %d, ok: %v\n", uids[i], segmentIDs[i], ok)
			}
			wg.Done()
		}(i)
	}

	wg.Wait()
}

func TestSDK_Check(t *testing.T) {
	mygClient := Init(6, 30*time.Second, 500*time.Millisecond, 50)
	raw := "00000000-0000-0000-0000-000000000001"

	for i := 136; i <= 186; i++ {
		if i == 163 {
			continue
		}

		go func(i int) {
			ok, err := mygClient.Check(raw, uint32(i), IdentifierTypeDeviceID)
			assert.Nil(t, err)
			assert.True(t, ok)
		}(i)
	}
}
