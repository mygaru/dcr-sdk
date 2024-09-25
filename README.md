# dcr-sdk

mygaru-client allows checking if UID is in a segment or to scan a list of them and see the overlap % between your list and the segment.

### Scanning
Note: the minimum limit of UIDs to send is 100.

```go
// init client with your partnerID
mygClient := Init(241, 30*time.Second)

segmentId := uint32(1000002)
uids := make([]string, 100)
for i := 0; i < 100; i++ {
    uids[i] = fmt.Sprintf("acefwevreger%d", i)
}

// in case your uid list is []string
inter1, err := mygClient.Scan(uids, segmentId)

uidsBytes := []byte(strings.Join(uids, ",\n"))
// in case your uid list is already encoded
inter2, err := mygClient.ScanReader(bytes.NewBuffer(uidsBytes), segmentId)

// in case you're reading the uids from a file, network response, etc.
inter3, err := mygClient.ScanBytes(uidsBytes, segmentId)
```