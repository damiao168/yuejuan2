package paper

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func referenceOrientationJPEG(t *testing.T, orientation int, order binary.ByteOrder) []byte {
	t.Helper()
	source := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 3; x++ {
			source.Set(x, y, color.NRGBA{R: uint8(30 + 70*x), G: uint8(30 + 150*y), B: uint8(20 + 25*x + 40*y), A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, source, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatal(err)
	}
	tiff := make([]byte, 26)
	if order == binary.LittleEndian {
		copy(tiff, "II")
	} else {
		copy(tiff, "MM")
	}
	order.PutUint16(tiff[2:4], 42)
	order.PutUint32(tiff[4:8], 8)
	order.PutUint16(tiff[8:10], 1)
	order.PutUint16(tiff[10:12], 0x0112)
	order.PutUint16(tiff[12:14], 3)
	order.PutUint32(tiff[14:18], 1)
	order.PutUint16(tiff[18:20], uint16(orientation))
	segment := append([]byte("Exif\x00\x00"), tiff...)
	result := []byte{0xff, 0xd8, 0xff, 0xe1, 0, byte(len(segment) + 2)}
	result = append(result, segment...)
	return append(result, encoded.Bytes()[2:]...)
}

func TestReferenceImageAllEXIFOrientations(t *testing.T) {
	// 独立列出输出像素顺序，覆盖镜像与旋转组合，防止宽高正确而裁图内容翻转。
	orders := map[int][]int{
		1: {0, 1, 2, 3, 4, 5}, 2: {2, 1, 0, 5, 4, 3},
		3: {5, 4, 3, 2, 1, 0}, 4: {3, 4, 5, 0, 1, 2},
		5: {0, 3, 1, 4, 2, 5}, 6: {3, 0, 4, 1, 5, 2},
		7: {5, 2, 4, 1, 3, 0}, 8: {2, 5, 1, 4, 0, 3},
	}
	for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		for orientation := 1; orientation <= 8; orientation++ {
			data := referenceOrientationJPEG(t, orientation, order)
			gotOrientation, err := referenceImageOrientation(data)
			if err != nil || gotOrientation != orientation {
				t.Fatalf("orientation %d: got %d, %v", orientation, gotOrientation, err)
			}
			source, err := jpeg.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			got, err := decodeOrientedReferenceImage(data)
			if err != nil {
				t.Fatal(err)
			}
			w, h := 3, 2
			if orientation >= 5 {
				w, h = 2, 3
			}
			if got.Bounds() != image.Rect(0, 0, w, h) {
				t.Fatalf("orientation %d: dimensions %v", orientation, got.Bounds())
			}
			for position, sourceIndex := range orders[orientation] {
				want := color.NRGBAModel.Convert(source.At(sourceIndex%3, sourceIndex/3))
				actual := color.NRGBAModel.Convert(got.At(position%w, position/w))
				if actual != want {
					t.Fatalf("orientation %d pixel %d: got %v want %v", orientation, position, actual, want)
				}
			}
		}
	}
}

func TestReferenceImageWithoutEXIFAndPNG(t *testing.T) {
	data := referenceOrientationJPEG(t, 1, binary.LittleEndian)
	data = append([]byte{0xff, 0xd8}, data[38:]...)
	if orientation, err := referenceImageOrientation(data); err != nil || orientation != 1 {
		t.Fatalf("JPEG without EXIF: %d, %v", orientation, err)
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewNRGBA(image.Rect(0, 0, 2, 3))); err != nil {
		t.Fatal(err)
	}
	if got, err := decodeOrientedReferenceImage(encoded.Bytes()); err != nil || got.Bounds() != image.Rect(0, 0, 2, 3) {
		t.Fatalf("PNG: %v, %v", got, err)
	}
}

func TestReferenceImageRejectsMalformedEXIF(t *testing.T) {
	valid := referenceOrientationJPEG(t, 6, binary.LittleEndian)
	cases := map[string]func([]byte) []byte{
		"byte order":          func(b []byte) []byte { b[12] = 'X'; return b },
		"TIFF marker":         func(b []byte) []byte { b[14] = 0; return b },
		"IFD offset":          func(b []byte) []byte { b[16] = 255; return b },
		"IFD count":           func(b []byte) []byte { b[20] = 255; return b },
		"tag type":            func(b []byte) []byte { b[24] = 4; return b },
		"tag count":           func(b []byte) []byte { b[26] = 2; return b },
		"zero orientation":    func(b []byte) []byte { b[30] = 0; return b },
		"unknown orientation": func(b []byte) []byte { b[30] = 9; return b },
		"EXIF header":         func(b []byte) []byte { b[10] = 1; return b },
		"truncated segment":   func(b []byte) []byte { return b[:25] },
		"conflicting APP1": func(b []byte) []byte {
			other := referenceOrientationJPEG(t, 3, binary.LittleEndian)
			return append(append(append([]byte{}, b[:38]...), other[2:38]...), b[38:]...)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			data := mutate(append([]byte{}, valid...))
			if _, err := referenceImageOrientation(data); err == nil {
				t.Fatal("damaged EXIF was accepted")
			}
			if _, err := decodeOrientedReferenceImage(data); err == nil {
				t.Fatal("damaged EXIF was decoded")
			}
		})
	}
}
