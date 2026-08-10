package service

import (
	"bytes"
	"image"
	"os"
	"testing"
)

func TestPrintPigoFace(t *testing.T) {
	b, err := os.ReadFile("/tmp/codex-original.png")
	if err != nil { t.Fatal(err) }
	im, _, err := image.Decode(bytes.NewReader(b))
	if err != nil { t.Fatal(err) }
	f, err := detectFaces(im)
	if err != nil { t.Fatal(err) }
	for i, face := range f { t.Logf("face[%d]=%+v", i, face) }
}
