package dcr_sdk

import (
	"github.com/stretchr/testify/assert"
	"sync"
	"testing"
	"time"
)

const token = "dsn"

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
