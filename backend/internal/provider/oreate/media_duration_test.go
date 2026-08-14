package oreate

import (
	"encoding/binary"
	"math"
	"testing"
)

func TestMP4DurationSeconds(t *testing.T) {
	tests := []struct {
		name     string
		version  byte
		scale    uint32
		duration uint64
		want     float64
	}{
		{"version-0", 0, 1000, 8250, 8.25},
		{"version-1", 1, 90000, 1125000, 12.5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := testMP4(tt.version, tt.scale, tt.duration)
			got, err := MP4DurationSeconds(data)
			if err != nil || math.Abs(got-tt.want) > 0.0001 {
				t.Fatalf("MP4DurationSeconds() = %v, %v; want %v", got, err, tt.want)
			}
		})
	}
}

func TestMP4DurationSecondsRejectsMalformedFiles(t *testing.T) {
	for _, data := range [][]byte{
		nil,
		makeISOBox("ftyp", []byte("isom")),
		makeISOBox("moov", makeISOBox("mvhd", []byte{0, 0, 0})),
		append([]byte{0, 0, 0, 32}, []byte("moov")...),
		testMP4(0, 0, 1000),
		testMP4(2, 1000, 1000),
	} {
		if _, err := MP4DurationSeconds(data); err == nil {
			t.Fatalf("MP4DurationSeconds(%x) unexpectedly succeeded", data)
		}
	}
}

func testMP4(version byte, timescale uint32, duration uint64) []byte {
	payloadSize := 20
	if version == 1 {
		payloadSize = 32
	}
	payload := make([]byte, payloadSize)
	payload[0] = version
	if version == 1 {
		binary.BigEndian.PutUint32(payload[20:24], timescale)
		binary.BigEndian.PutUint64(payload[24:32], duration)
	} else {
		binary.BigEndian.PutUint32(payload[12:16], timescale)
		binary.BigEndian.PutUint32(payload[16:20], uint32(duration))
	}
	return append(makeISOBox("ftyp", []byte("isom")), makeISOBox("moov", makeISOBox("mvhd", payload))...)
}

func makeISOBox(kind string, payload []byte) []byte {
	box := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(box[0:4], uint32(len(box)))
	copy(box[4:8], kind)
	copy(box[8:], payload)
	return box
}
