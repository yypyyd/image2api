package service

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestApplyReferenceFaceSwapKeepsSingleReferenceCompatible(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			img.SetRGBA(x, y, color.RGBA{R: 180, G: 180, B: 180, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	raw := base64.StdEncoding.EncodeToString(encoded.Bytes())
	out, err := applyReferenceFaceSwap([]string{raw})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0] != raw {
		t.Fatalf("single reference should pass through unchanged: %#v", out)
	}
}

func TestDecodeReferenceImageAcceptsDataURI(t *testing.T) {
	raw := base64.StdEncoding.EncodeToString([]byte("payload"))
	out, err := decodeReferenceImage("data:image/png;base64," + raw)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "payload" {
		t.Fatalf("unexpected decoded payload %q", out)
	}
}

func TestPasteFaceUsesPigoRectangle(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			src.SetRGBA(x, y, color.RGBA{R: uint8(x * 60), G: uint8(y * 60), B: 20, A: 255})
		}
	}
	dst := image.NewRGBA(image.Rect(0, 0, 6, 6))
	tone := image.NewRGBA(dst.Bounds())
	if err := pasteFace(dst, tone, src, image.Rect(0, 0, 4, 4), image.Rect(1, 1, 5, 5)); err != nil {
		t.Fatal(err)
	}
	// The corners are part of Pigo's rectangular crop; an oval/feathered mask
	// would leave them untouched.
	if got := dst.RGBAAt(1, 1); got.A == 0 {
		t.Fatalf("rectangular crop corner was discarded: %#v", got)
	}
}

func TestSwapFaceHalvesMovesLeftToRightAndRightToLeft(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 8, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			src.SetRGBA(x, y, color.RGBA{R: 240, A: 255})
		}
		for x := 4; x < 8; x++ {
			src.SetRGBA(x, y, color.RGBA{B: 240, A: 255})
		}
	}
	out, _, err := swapFaceHalvesRaw(src, detectedFace{box: image.Rect(0, 0, 8, 4)})
	if err != nil {
		t.Fatal(err)
	}
	if got := out.RGBAAt(1, 1); got.B < 200 || got.R > 50 {
		t.Fatalf("right panel was not moved to the left: %#v", got)
	}
	if got := out.RGBAAt(6, 1); got.R < 200 || got.B > 50 {
		t.Fatalf("left panel was not moved to the right: %#v", got)
	}
}
