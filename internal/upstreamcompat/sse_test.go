package upstreamcompat_test

import (
	"reflect"
	"testing"

	"github.com/0xForce-Network/simple-sub2api/internal/upstreamcompat"
)

func TestForEachOpenAISSEDataPayload(t *testing.T) {
	body := "event: message\ndata: {\"id\":\"a\"}\n\ndata: [DONE]\n\n"
	var got []string
	upstreamcompat.ForEachOpenAISSEDataPayload(body, func(payload []byte) {
		got = append(got, string(payload))
	})
	if !reflect.DeepEqual(got, []string{"{\"id\":\"a\"}"}) {
		t.Fatalf("payloads = %#v", got)
	}
}

func TestMultiLineOpenAISSEDataPayload(t *testing.T) {
	body := "data: {\"a\":\n" + "data: 1}\n\n"
	var got []string
	upstreamcompat.ForEachOpenAISSEDataPayload(body, func(payload []byte) {
		got = append(got, string(payload))
	})
	if !reflect.DeepEqual(got, []string{"{\"a\":\n1}"}) {
		t.Fatalf("payloads = %#v", got)
	}
}
