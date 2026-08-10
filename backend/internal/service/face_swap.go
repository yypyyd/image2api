package service

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg"
	"image/png"
	"sort"
	"strings"
	"sync"

	"github.com/esimov/pigo/core"
	xdraw "golang.org/x/image/draw"
)

// facefinder is Pigo's compact binary cascade. Keeping it in the binary makes
// the preprocessing deterministic in Docker and avoids a runtime model download.
//
//go:embed cascade/facefinder
var facefinder []byte

var faceDetector struct {
	sync.Once
	classifier *pigo.Pigo
	err        error
}

type detectedFace struct {
	box   image.Rectangle
	score float32
}

// applyReferenceFaceSwap crops the first face detected by Pigo and exchanges
// its left and right vertical panels in the same image. No external face is
// ever introduced. References without a reliable face remain unchanged.
func applyReferenceFaceSwap(inputs []string) ([]string, error) {
	out := make([]string, 0, len(inputs))
	for _, raw := range inputs {
		data, err := decodeReferenceImage(raw)
		if err != nil {
			return nil, err
		}
		src, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("%w: reference image is not decodable", ErrUnsupportedParams)
		}
		faces, err := detectFaces(src)
		if err != nil {
			if strings.Contains(err.Error(), "no face") {
				out = append(out, raw)
				continue
			}
			return nil, fmt.Errorf("%w: %v", ErrUnsupportedParams, err)
		}
		composite, err := swapFaceHalvesWithinImage(src, faces[0])
		if err != nil {
			return nil, err
		}
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, composite); err != nil {
			return nil, fmt.Errorf("%w: encode face-panel swap: %v", ErrUnsupportedParams, err)
		}
		if encoded.Len() > maxReferenceImageBytes {
			return nil, ErrReferenceTooLarge
		}
		out = append(out, base64.StdEncoding.EncodeToString(encoded.Bytes()))
	}
	return out, nil
}

// swapFaceHalvesWithinImage keeps the Pigo face rectangle fixed, splits it at
// the vertical midpoint, and moves the right panel to the left and the left
// panel to the right. This is deliberately a hard rectangular operation: the
// result is the vertical-panel face transformation requested by the client.
func swapFaceHalvesWithinImage(src image.Image, face detectedFace) (*image.RGBA, error) {
	// Mask the eye/glasses band on the detected face before exchanging panels.
	// This keeps the black strip tied to the original facial landmarks instead
	// of stretching it across the surrounding hair and background.
	prepared := image.NewRGBA(src.Bounds())
	draw.Draw(prepared, prepared.Bounds(), src, src.Bounds().Min, draw.Src)
	applyEyeBars(prepared, face.box)
	dst, panelBox, err := swapFaceHalvesRaw(prepared, face)
	if err != nil {
		return nil, err
	}
	// Veil the complete exchanged panel, including the expanded hair and
	// surrounding context, so no recognizable face fragment remains outside
	// the grid.
	applyFaceGrid(dst, panelBox)
	return dst, nil
}

func swapFaceHalvesRaw(src image.Image, face detectedFace) (*image.RGBA, image.Rectangle, error) {
	bounds := src.Bounds()
	if bounds.Dx() <= 0 || bounds.Dy() <= 0 || int64(bounds.Dx())*int64(bounds.Dy()) > maxReferencePixels {
		return nil, image.Rectangle{}, ErrReferenceTooLarge
	}
	box := expandFacePanelBox(face.box.Intersect(image.Rect(0, 0, bounds.Dx(), bounds.Dy())), image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	if box.Dx() < 4 || box.Dy() < 2 {
		return nil, image.Rectangle{}, fmt.Errorf("%w: detected face is too small", ErrUnsupportedParams)
	}

	dst := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(dst, dst.Bounds(), src, bounds.Min, draw.Src)
	crop := image.NewRGBA(image.Rect(0, 0, box.Dx(), box.Dy()))
	draw.Draw(crop, crop.Bounds(), src, box.Min, draw.Src)

	leftWidth := crop.Bounds().Dx() / 2
	left := crop.SubImage(image.Rect(0, 0, leftWidth, crop.Bounds().Dy()))
	right := crop.SubImage(image.Rect(leftWidth, 0, crop.Bounds().Dx(), crop.Bounds().Dy()))

	leftTarget := image.Rect(box.Min.X, box.Min.Y, box.Min.X+leftWidth, box.Max.Y)
	rightTarget := image.Rect(box.Min.X+leftWidth, box.Min.Y, box.Max.X, box.Max.Y)
	leftPanel := image.NewRGBA(image.Rect(0, 0, leftTarget.Dx(), leftTarget.Dy()))
	rightPanel := image.NewRGBA(image.Rect(0, 0, rightTarget.Dx(), rightTarget.Dy()))
	// The odd-pixel case has different half widths, so scale each source panel
	// into the other panel's exact rectangle while preserving the hard seam.
	xdraw.CatmullRom.Scale(leftPanel, leftPanel.Bounds(), right, right.Bounds(), xdraw.Src, nil)
	xdraw.CatmullRom.Scale(rightPanel, rightPanel.Bounds(), left, left.Bounds(), xdraw.Src, nil)
	draw.Draw(dst, leftTarget, leftPanel, image.Point{}, draw.Src)
	draw.Draw(dst, rightTarget, rightPanel, image.Point{}, draw.Src)
	return dst, box, nil
}

// expandFacePanelBox makes the exchanged panels larger than the tight Pigo
// face square. The upward expansion deliberately includes hair while the
// smaller side/bottom expansion keeps the surrounding composition stable.
func expandFacePanelBox(box, bounds image.Rectangle) image.Rectangle {
	if box.Dx() <= 0 || box.Dy() <= 0 {
		return box
	}
	padX := max(1, int(float64(box.Dx())*0.18))
	padTop := max(1, int(float64(box.Dy())*0.55))
	padBottom := max(1, int(float64(box.Dy())*0.12))
	return image.Rect(
		max(bounds.Min.X, box.Min.X-padX),
		max(bounds.Min.Y, box.Min.Y-padTop),
		min(bounds.Max.X, box.Max.X+padX),
		min(bounds.Max.Y, box.Max.Y+padBottom),
	)
}

// applyFaceGrid draws a dark, regular grid over the complete exchanged panel
// after the two vertical panels have exchanged positions. The lines are
// translucent enough to preserve the panel composition while breaking up
// facial landmarks across the whole hair-inclusive crop.
func applyFaceGrid(dst *image.RGBA, box image.Rectangle) {
	box = box.Intersect(dst.Bounds())
	if box.Dx() < 2 || box.Dy() < 2 {
		return
	}
	cell := max(18, min(box.Dx(), box.Dy())/10)
	lineWidth := max(3, cell/10)
	const overlayAlpha = 220
	for y := box.Min.Y; y < box.Max.Y; y += cell {
		for yy := y; yy < min(box.Max.Y, y+lineWidth); yy++ {
			for x := box.Min.X; x < box.Max.X; x++ {
				darkenPixel(dst, x, yy, overlayAlpha)
			}
		}
	}
	for x := box.Min.X; x < box.Max.X; x += cell {
		for xx := x; xx < min(box.Max.X, x+lineWidth); xx++ {
			for y := box.Min.Y; y < box.Max.Y; y++ {
				darkenPixel(dst, xx, y, overlayAlpha)
			}
		}
	}
}

func darkenPixel(dst *image.RGBA, x, y, alpha int) {
	p := dst.RGBAAt(x, y)
	inv := 255 - alpha
	dst.SetRGBA(x, y, color.RGBA{
		R: uint8((int(p.R) * inv) / 255),
		G: uint8((int(p.G) * inv) / 255),
		B: uint8((int(p.B) * inv) / 255),
		A: p.A,
	})
}

// applyEyeBars draws two nearly opaque horizontal black strips over the
// estimated left and right eye regions. Keeping each strip inside one half of
// the original face prevents it from spilling across the exchanged panels.
func applyEyeBars(dst *image.RGBA, faceBox image.Rectangle) {
	faceBox = faceBox.Intersect(dst.Bounds())
	if faceBox.Dx() < 2 || faceBox.Dy() < 2 {
		return
	}
	centerY := faceBox.Min.Y + int(float64(faceBox.Dy())*0.36)
	barHeight := max(8, int(float64(faceBox.Dy())*0.15))
	barWidth := max(10, int(float64(faceBox.Dx())*0.27))
	leftCenter := faceBox.Min.X + int(float64(faceBox.Dx())*0.32)
	rightCenter := faceBox.Min.X + int(float64(faceBox.Dx())*0.68)
	drawEyeBar(dst, faceBox, leftCenter, centerY, barWidth, barHeight)
	drawEyeBar(dst, faceBox, rightCenter, centerY, barWidth, barHeight)
}

func drawEyeBar(dst *image.RGBA, faceBox image.Rectangle, centerX, centerY, width, height int) {
	startX := max(faceBox.Min.X, centerX-width/2)
	endX := min(faceBox.Max.X, startX+width)
	startY := max(faceBox.Min.Y, centerY-height/2)
	endY := min(faceBox.Max.Y, startY+height)
	const barAlpha = 245
	for y := startY; y < endY; y++ {
		for x := startX; x < endX; x++ {
			darkenPixel(dst, x, y, barAlpha)
		}
	}
}

func decodeReferenceImage(raw string) ([]byte, error) {
	value := strings.TrimSpace(raw)
	if comma := bytes.IndexByte([]byte(value), ','); comma >= 0 {
		value = strings.TrimSpace(value[comma+1:])
	}
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(value)
	}
	if err != nil || len(data) == 0 {
		return nil, fmt.Errorf("%w: invalid face image encoding", ErrUnsupportedParams)
	}
	if len(data) > maxReferenceImageBytes {
		return nil, ErrReferenceTooLarge
	}
	return data, nil
}

func swapFacesWithinImage(src image.Image, first, second detectedFace) (*image.RGBA, error) {
	bounds := src.Bounds()
	if bounds.Dx() <= 0 || bounds.Dy() <= 0 || int64(bounds.Dx())*int64(bounds.Dy()) > maxReferencePixels {
		return nil, ErrReferenceTooLarge
	}
	dst := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(dst, dst.Bounds(), src, bounds.Min, draw.Src)
	base := image.NewRGBA(dst.Bounds())
	draw.Draw(base, base.Bounds(), dst, image.Point{}, draw.Src)

	// Keep Pigo's rectangle exactly. Do not synthesize a new face, enlarge the
	// crop, or feather it into an oval: callers use this preprocessing to break
	// the identity in the reference image, and the detector's crop is the only
	// source region that may be copied.
	firstBox := first.box
	secondBox := second.box
	if err := pasteFace(dst, base, src, secondBox, firstBox); err != nil {
		return nil, err
	}
	if err := pasteFace(dst, base, src, firstBox, secondBox); err != nil {
		return nil, err
	}
	return dst, nil
}

func pasteFace(dst, toneTarget *image.RGBA, src image.Image, sourceBox, targetBox image.Rectangle) error {
	if sourceBox.Dx() < 2 || sourceBox.Dy() < 2 || targetBox.Dx() < 2 || targetBox.Dy() < 2 {
		return fmt.Errorf("%w: detected face is too small", ErrUnsupportedParams)
	}
	srcCrop := image.NewRGBA(sourceBox)
	draw.Draw(srcCrop, srcCrop.Bounds(), src, sourceBox.Min, draw.Src)
	face := image.NewRGBA(image.Rect(0, 0, targetBox.Dx(), targetBox.Dy()))
	xdraw.CatmullRom.Scale(face, face.Bounds(), srcCrop, srcCrop.Bounds(), xdraw.Over, nil)
	correctFaceTone(face, toneTarget, targetBox)

	// Pigo returns a rectangular crop. Copy that crop as a rectangle, preserving
	// the hard panel boundaries in references such as split/strip artwork.
	draw.Draw(dst, targetBox, face, image.Point{}, draw.Src)
	return nil
}

// correctFaceTone applies a restrained per-channel offset so swapped faces
// keep the target's lighting instead of looking like pasted foreign pixels.
func correctFaceTone(face, toneTarget *image.RGBA, targetBox image.Rectangle) {
	var sourceSum, targetSum [3]uint64
	var count uint64
	for y := 0; y < face.Bounds().Dy(); y++ {
		for x := 0; x < face.Bounds().Dx(); x++ {
			nx := (float64(x)+0.5)/float64(face.Bounds().Dx())*2 - 1
			ny := (float64(y)+0.5)/float64(face.Bounds().Dy())*2 - 1
			if nx*nx+ny*ny > 0.42 {
				continue
			}
			r, g, b, _ := face.At(x, y).RGBA()
			tr, tg, tb, _ := toneTarget.At(targetBox.Min.X+x, targetBox.Min.Y+y).RGBA()
			sourceSum[0] += uint64(r >> 8)
			sourceSum[1] += uint64(g >> 8)
			sourceSum[2] += uint64(b >> 8)
			targetSum[0] += uint64(tr >> 8)
			targetSum[1] += uint64(tg >> 8)
			targetSum[2] += uint64(tb >> 8)
			count++
		}
	}
	if count == 0 {
		return
	}
	var delta [3]int
	for i := range delta {
		delta[i] = int(float64(int64(targetSum[i]/count)-int64(sourceSum[i]/count)) * 0.72)
	}
	for y := 0; y < face.Bounds().Dy(); y++ {
		for x := 0; x < face.Bounds().Dx(); x++ {
			r, g, b, a := face.At(x, y).RGBA()
			face.SetRGBA(x, y, color.RGBA{
				R: clampByte(int(r>>8) + delta[0]),
				G: clampByte(int(g>>8) + delta[1]),
				B: clampByte(int(b>>8) + delta[2]),
				A: uint8(a >> 8),
			})
		}
	}
}

func clampByte(v int) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

func detectFaces(src image.Image) ([]detectedFace, error) {
	faces, primaryErr := runPigoFaces(src)
	faces = filterFaceCandidates(faces, src.Bounds())
	if len(faces) >= 2 {
		return faces, nil
	}
	// AI artwork often cuts a face at a panel edge. Run Pigo on a few enlarged
	// vertical windows, including a mirrored edge pad, so the cascade sees a
	// complete candidate without changing the original pixels we crop from.
	windowFaces, windowErr := detectFacesInWindows(src)
	faces = filterFaceCandidates(mergeDetectedFaces(append(faces, windowFaces...)), src.Bounds())
	if len(faces) > 0 {
		return faces, nil
	}
	if primaryErr != nil {
		return nil, primaryErr
	}
	return nil, windowErr
}

func filterFaceCandidates(faces []detectedFace, bounds image.Rectangle) []detectedFace {
	minSide := max(24, int(float64(min(bounds.Dx(), bounds.Dy()))*0.18))
	out := make([]detectedFace, 0, len(faces))
	for _, face := range faces {
		if min(face.box.Dx(), face.box.Dy()) >= minSide {
			out = append(out, face)
		}
	}
	return out
}

func runPigoFaces(src image.Image) ([]detectedFace, error) {
	faceDetector.Do(func() {
		faceDetector.classifier, faceDetector.err = pigo.NewPigo().Unpack(facefinder)
	})
	if faceDetector.err != nil {
		return nil, faceDetector.err
	}
	bounds := src.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("image has no pixels")
	}
	minSize := max(20, min(width, height)/8)
	maxSize := min(width, height)
	dets := faceDetector.classifier.RunCascade(pigo.CascadeParams{
		ImageParams: pigo.ImageParams{
			Pixels: pigo.RgbToGrayscale(src),
			Rows:   height,
			Cols:   width,
			Dim:    width,
		},
		MinSize:     minSize,
		MaxSize:     maxSize,
		ShiftFactor: 0.1,
		ScaleFactor: 1.1,
	}, 0.0)
	if len(dets) == 0 {
		return nil, fmt.Errorf("no face detected")
	}
	dets = faceDetector.classifier.ClusterDetections(dets, 0.2)
	faces := make([]detectedFace, 0, len(dets))
	for _, det := range dets {
		half := det.Scale / 2
		left := max(0, det.Col-half)
		top := max(0, det.Row-half)
		right := min(width, det.Col+det.Scale-half)
		bottom := min(height, det.Row+det.Scale-half)
		if right-left >= 2 && bottom-top >= 2 {
			faces = append(faces, detectedFace{box: image.Rect(left, top, right, bottom), score: det.Q})
		}
	}
	if len(faces) == 0 {
		return nil, fmt.Errorf("no usable face detected")
	}
	sort.Slice(faces, func(i, j int) bool { return faces[i].box.Dx()*faces[i].box.Dy() > faces[j].box.Dx()*faces[j].box.Dy() })
	return faces, nil
}

func detectFacesInWindows(src image.Image) ([]detectedFace, error) {
	bounds := src.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	windowW := max(180, int(float64(width)*0.43))
	windowH := max(240, int(float64(height)*0.68))
	windowW = min(width, windowW)
	windowH = min(height, windowH)
	if windowW < 20 || windowH < 20 {
		return nil, fmt.Errorf("no face detected")
	}
	starts := []int{0, int(float64(width) * 0.15), max(0, width-windowW)}
	var found []detectedFace
	for _, start := range starts {
		if start+windowW > width {
			start = width - windowW
		}
		cropRect := image.Rect(start, 0, start+windowW, windowH)
		crop := image.NewRGBA(image.Rect(0, 0, cropRect.Dx(), cropRect.Dy()))
		draw.Draw(crop, crop.Bounds(), src, cropRect.Min, draw.Src)
		found = append(found, remapWindowFaces(crop, start, 0, 4, runScaledPigo(crop, false))...)

		// Mirror only the visible part near the right edge. This lets Pigo
		// classify a half-face panel, but the resulting crop is still taken from
		// the original image coordinates.
		if cropRect.Max.X == width {
			axis := max(20, int(float64(crop.Bounds().Dx())*0.47))
			pad := mirrorPad(crop, axis)
			found = append(found, remapWindowFaces(pad, start, 0, 4, runScaledPigo(pad, false))...)
		}
	}
	found = mergeDetectedFaces(found)
	if len(found) == 0 {
		return nil, fmt.Errorf("no face detected")
	}
	return found, nil
}

func runScaledPigo(src image.Image, _ bool) []detectedFace {
	const scale = 4
	big := image.NewRGBA(image.Rect(0, 0, src.Bounds().Dx()*scale, src.Bounds().Dy()*scale))
	xdraw.CatmullRom.Scale(big, big.Bounds(), src, src.Bounds(), xdraw.Src, nil)
	faces, _ := runPigoFaces(big)
	return faces
}

func remapWindowFaces(crop image.Image, offsetX, offsetY, scale int, faces []detectedFace) []detectedFace {
	out := make([]detectedFace, 0, len(faces))
	for _, face := range faces {
		cx := float64(face.box.Min.X+face.box.Max.X) / 2 / float64(scale)
		cy := float64(face.box.Min.Y+face.box.Max.Y) / 2 / float64(scale)
		size := float64(max(face.box.Dx(), face.box.Dy())) / float64(scale)
		left := max(0, int(cx-size/2)+offsetX)
		top := max(0, int(cy-size/2)+offsetY)
		right := min(offsetX+crop.Bounds().Dx(), int(cx+size/2)+offsetX)
		bottom := min(offsetY+crop.Bounds().Dy(), int(cy+size/2)+offsetY)
		if right-left >= 2 && bottom-top >= 2 {
			out = append(out, detectedFace{box: image.Rect(left, top, right, bottom), score: face.score})
		}
	}
	return out
}

func mirrorPad(src image.Image, axis int) image.Image {
	axis = min(src.Bounds().Dx(), max(1, axis))
	dst := image.NewRGBA(image.Rect(0, 0, axis*2, src.Bounds().Dy()))
	for y := 0; y < dst.Bounds().Dy(); y++ {
		for x := 0; x < axis; x++ {
			dst.Set(x, y, src.At(x, y))
		}
		for x := axis; x < axis*2; x++ {
			dst.Set(x, y, src.At(max(0, min(src.Bounds().Dx()-1, 2*axis-1-x)), y))
		}
	}
	return dst
}

func mergeDetectedFaces(faces []detectedFace) []detectedFace {
	merged := make([]detectedFace, 0, len(faces))
	for _, candidate := range faces {
		duplicate := false
		for _, existing := range merged {
			if faceIoU(candidate.box, existing.box) > 0.35 {
				duplicate = true
				break
			}
		}
		if !duplicate {
			merged = append(merged, candidate)
		}
	}
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].box.Dx()*merged[i].box.Dy() > merged[j].box.Dx()*merged[j].box.Dy()
	})
	return merged
}

func faceIoU(a, b image.Rectangle) float64 {
	intersection := a.Intersect(b)
	if intersection.Empty() {
		return 0
	}
	area := float64(a.Dx()*a.Dy() + b.Dx()*b.Dy() - intersection.Dx()*intersection.Dy())
	if area <= 0 {
		return 0
	}
	return float64(intersection.Dx()*intersection.Dy()) / area
}
