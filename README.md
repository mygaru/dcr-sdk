# dcr-sdk

```sh
GOPRIVATE=github.com go get github.com/mygaru/dcr-sdk/myg-client
```


mygaru-client allows checking if an user (represented by either a DeviceID, PartnerUID (PUID), or OTP) is in a segment 
or to scan a list of PUIDs and see the overlap % between your list and the segment.

### Scan
Note: the minimum limit of UIDs to send is 100.

```go
// init client with your partnerID, HTTP timeout, batch timeout & queue size
mygClient := Init(6, 30*time.Second, 500*time.Millisecond, 50)

segmentId := uint32(163)
uids := make([]string, 100)
for i := 0; i < 100; i++ {
uids[i] = fmt.Sprintf("acefwevreger%d", i)
}

// in case your uid list is []string
inter1, err := mygClient.Scan(uids, segmentId)

// in case you're reading the uids from a file, network response, etc.
inter2, err := mygClient.ScanReader(bytes.NewBuffer(uidsBytes), segmentId)

uidsBytes := []byte(strings.Join(uids, ",\n"))
// in case your uid list is already encoded
inter3, err := mygClient.ScanBytes(uidsBytes, segmentId)



```

### Check

Note: check calls are batched according to the batch timeout and queue size you provide.
```go
mygClient := Init(6, 30*time.Second, 500*time.Millisecond, 50)
ok, err := mygClient.Check("00000000-0000-0000-0000-000000000001", 163, IdentifierTypeDeviceID)
```
