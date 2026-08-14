package oreate

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// MP4DurationSeconds reads the movie-header duration from an ISO BMFF file.
// Oreate accepts MP4/MOV references; parsing mvhd keeps production images free
// of an ffprobe runtime dependency.
func MP4DurationSeconds(data []byte) (float64, error) {
	moov, err := findISOBox(data, "moov")
	if err != nil {
		return 0, fmt.Errorf("oreate: invalid reference video: %w", err)
	}
	mvhd, err := findISOBox(moov, "mvhd")
	if err != nil {
		return 0, fmt.Errorf("oreate: invalid reference video: %w", err)
	}
	if len(mvhd) < 20 {
		return 0, errors.New("oreate: invalid reference video: truncated mvhd box")
	}
	var timescale uint32
	var duration uint64
	switch mvhd[0] {
	case 0:
		timescale = binary.BigEndian.Uint32(mvhd[12:16])
		duration = uint64(binary.BigEndian.Uint32(mvhd[16:20]))
	case 1:
		if len(mvhd) < 32 {
			return 0, errors.New("oreate: invalid reference video: truncated version 1 mvhd box")
		}
		timescale = binary.BigEndian.Uint32(mvhd[20:24])
		duration = binary.BigEndian.Uint64(mvhd[24:32])
	default:
		return 0, errors.New("oreate: invalid reference video: unsupported mvhd version")
	}
	if timescale == 0 || duration == 0 {
		return 0, errors.New("oreate: invalid reference video: missing duration")
	}
	seconds := float64(duration) / float64(timescale)
	if seconds <= 0 || math.IsInf(seconds, 0) || math.IsNaN(seconds) {
		return 0, errors.New("oreate: invalid reference video: invalid duration")
	}
	return seconds, nil
}

func findISOBox(data []byte, wanted string) ([]byte, error) {
	for offset := 0; offset < len(data); {
		if len(data)-offset < 8 {
			return nil, errors.New("truncated box header")
		}
		size := uint64(binary.BigEndian.Uint32(data[offset : offset+4]))
		headerSize := uint64(8)
		if size == 1 {
			if len(data)-offset < 16 {
				return nil, errors.New("truncated extended box header")
			}
			size = binary.BigEndian.Uint64(data[offset+8 : offset+16])
			headerSize = 16
		} else if size == 0 {
			size = uint64(len(data) - offset)
		}
		if size < headerSize || size > uint64(len(data)-offset) {
			return nil, errors.New("invalid box size")
		}
		end := offset + int(size)
		if string(data[offset+4:offset+8]) == wanted {
			return data[offset+int(headerSize) : end], nil
		}
		offset = end
	}
	return nil, fmt.Errorf("missing %s box", wanted)
}
