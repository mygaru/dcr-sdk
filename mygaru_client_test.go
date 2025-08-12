package dcr_sdk

import (
	"bytes"
	"fmt"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"log"
	"strings"
	"sync"
	"testing"
	"time"
)

const token = "dsn"

func TestSDK_Scan(t *testing.T) {
	mygClient := Init([]byte(token), 30*time.Second, 5*time.Second, 300)
	segmentId := uint32(413)

	puids := []string{"pFSTkoL1jP3+/WPc46J5AV3PZ+r679sPtw4wYwM8KXj9"}

	inter1, err := mygClient.Scan(puids, segmentId)
	assert.Nil(t, err)

	puidsBytes := []byte(strings.Join(puids, ",\n"))
	inter2, err := mygClient.ScanReader(bytes.NewBuffer(puidsBytes), segmentId)
	assert.Nil(t, err)

	inter3, err := mygClient.ScanBytes(puidsBytes, segmentId)
	assert.Nil(t, err)

	log.Println(inter1, inter2, inter3)
	assert.True(t, inter1 == inter2 && inter3 == inter1)
}

func TestSDK_Check_Multiple(t *testing.T) {
	mygClient := Init([]byte(token), 30*time.Second, 500*time.Millisecond, 50)
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
	mygClient := Init([]byte(token), 30*time.Second, 500*time.Millisecond, 50)
	raw := "00000000-0000-0000-0000-000000000001"

	wg := sync.WaitGroup{}

	for i := 136; i <= 186; i++ {
		if i == 163 || i == 155 {
			continue
		}
		wg.Add(1)

		go func(i int) {
			ok, err := mygClient.Check(raw, uint32(i), IdentifierTypeDeviceID)
			assert.Nil(t, err)
			assert.True(t, ok)
			wg.Done()
		}(i)
	}
	wg.Wait()
}

func TestSDK_Check2(t *testing.T) {
	mygClient := Init([]byte(token), 30*time.Second, 500*time.Millisecond, 50)
	raw := "259f835567d099ee"

	ok, err := mygClient.Check(raw, 307, IdentifierTypeExternalUID)
	assert.Nil(t, err)
	assert.True(t, ok)
}
