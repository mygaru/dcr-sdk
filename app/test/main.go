package main

import (
	"flag"
	"fmt"

	"github.com/google/uuid"
	dcr_sdk "github.com/mygaru/dcr-sdk"
	base "github.com/mygaru/dcr-sdk/gen/base1"
	"github.com/mygaru/dcr-sdk/pkg/client"
)

var dcrToken = flag.String("dcrToken", "Bearer eyJhbGciOiJSUzI1NiIsInR5cCIgOiAiSldUIiwia2lkIiA6ICJKb01pUnNCMkNWZEs2YzVvU3Jvc2VzTkMxT3U1aDN1dkQ1VElmYkluNldRIn0.eyJleHAiOjE3OTA2NzU2MjQsImlhdCI6MTc1OTEzOTYyNCwiYXV0aF90aW1lIjoxNzU5MTM5NjIzLCJqdGkiOiJvZnJ0YWM6OGFmNWMxYmItMDNhMS0wODZkLWNiYTEtNjZmMzQ1OTkzMmRmIiwiaXNzIjoiaHR0cHM6Ly9hdXRoLm15Z2FydS5jb20vcmVhbG1zL0RDUiIsInN1YiI6ImZlZjNhMjgxLWNjYjQtNGE5YS1hNDQ5LTVhZDE1M2U5ZmFmNCIsInR5cCI6IkJlYXJlciIsImF6cCI6ImRzcC1nbG9iYWwtZGlnaXRhbCIsInNpZCI6ImM0MzM1Y2RhLTFhMzgtNDg3MC1iYzg4LTYyNjcwMmY4YjhkNyIsImFjciI6IjEiLCJzY29wZSI6Im9wZW5pZCBkY3IuY2xvdWQuc2VnbWVudHMuc2NhbiBkY3IuY2xvdWQuc2VnbWVudHMudG91Y2ggZGNyLnBsYXRmb3JtLnNlZ21lbnRzLnJlYWQgZGNyLmNsb3VkLnNlZ21lbnRzLmNyZWF0ZSBwcm9maWxlIGVtYWlsIHVpZCBwaWQgZGNyLnBsYXRmb3JtLnNlZ21lbnRzLnN0b3JlIG9mZmxpbmVfYWNjZXNzIiwidWlkIjoiOThmYTMxNGMtMTg2Ni00YjliLTliNTQtMDM2OGNlOGFhM2M5IiwiZW1haWxfdmVyaWZpZWQiOmZhbHNlLCJuYW1lIjoiR2xvYmFsIERpZ2l0YWwgR2xvYmFsIERpZ2l0YWwiLCJwaWQiOiJiMWZkY2U4My05MjlkLTRkNGQtOTc3NC1jNjA2Mjk1ZmU3MWQiLCJwcmVmZXJyZWRfdXNlcm5hbWUiOiI5OGZhMzE0Yy0xODY2LTRiOWItOWI1NC0wMzY4Y2U4YWEzYzkiLCJnaXZlbl9uYW1lIjoiR2xvYmFsIERpZ2l0YWwiLCJmYW1pbHlfbmFtZSI6Ikdsb2JhbCBEaWdpdGFsIiwiZW1haWwiOiJnbG9iYWwtZGlnaXRhbEBtYWlsLmNvbSJ9.hROZw0TxhZ32rs5291og_r0Y2-3enErh3KJ-rQEC4CrDNA485gl_E_uKlsax70yFySbv45hT3bNveDsJNk_8o2LnxyzQmLd4ZifuF8p-VfSkKS8P0HWfuEgT25J1hhwl-t45jtiCuaqoUozNmL8ajN27s5TwBkl3kUskGynAC7tA1eV_r7n5Kn7jpDWfwvVbnHfG6o54OiKXUIqAMO0F1Z130BYUlV4PEk8liKYui3wJVfqlnVxaJEPIkm98tlbXewODO7RRedzMzUq5Snw0hG8yyifltIJN_oM6DTyXmdk0KVsPOEpUM48mCjvnBzBif6MInXjLER3V1rCJYs87rw", "DCR token")

func main() {
	/*
		178.63.252.110:7937
		178.63.252.111:7937
		178.63.252.112:7937
	*/
	cl := dcr_sdk.New(&client.Configuration{
		JvtToken: []byte(*dcrToken),
		Addrs:    "cloud.mygaru.com:7937",
	})

	for i := 0; i < 2; i++ {
		resp, statusCode, err := cl.Target(&base.TargetRequest{
			Uids: []*base.UID{
				{Id: []byte(uuid.New().String()), Type: base.UID_DEVICE_ID},
			},
			Match: []*base.Match_Rule{
				{TrafficType: base.TrafficType_TRAFFIC_TYPE_VIDEO, SegmentIds: []uint32{1, 2, 3}},
			},
		})
		fmt.Println(fmt.Sprintf("%+v", resp), statusCode, err)

	}

}
