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
	mygClient := Init(6, 30*time.Second, 500*time.Millisecond, 50)
	n := 100
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
			ok, err := mygClient.Check(uids[i], segmentIDs[i], IdentifierTypeExternal)
			assert.Nil(t, err)
			if err == nil {
				fmt.Printf("uid: %s, seg: %d, ok: %v\n", uids[i], segmentIDs[i], ok)
			}
			wg.Done()
		}(i)
	}

	wg.Wait()
}
