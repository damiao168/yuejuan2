package paper

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
)

// 参考图的显示尺寸和裁图坐标都以方向归一化后的像素为准。JPEG 的
// EXIF 损坏时不能猜成方向 1，否则浏览器显示和服务端裁图可能指向不同内容。
func referenceImageOrientation(data []byte) (int, error) {
	if bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) {
		return 1, nil
	}
	if len(data) < 2 || data[0] != 0xff || data[1] != 0xd8 {
		return 0, fmt.Errorf("reference image must be PNG or JPEG")
	}
	orientation, found := 1, false
	for pos := 2; pos < len(data); {
		if data[pos] != 0xff {
			return 0, fmt.Errorf("invalid JPEG marker")
		}
		for pos < len(data) && data[pos] == 0xff {
			pos++
		}
		if pos >= len(data) {
			return 0, fmt.Errorf("truncated JPEG marker")
		}
		marker := data[pos]
		pos++
		if marker == 0xda || marker == 0xd9 {
			return orientation, nil
		}
		if marker == 0x01 || marker >= 0xd0 && marker <= 0xd7 {
			continue
		}
		if marker == 0x00 || marker == 0xd8 || pos+2 > len(data) {
			return 0, fmt.Errorf("invalid JPEG segment")
		}
		size := int(binary.BigEndian.Uint16(data[pos : pos+2]))
		if size < 2 || size > len(data)-pos {
			return 0, fmt.Errorf("truncated JPEG segment")
		}
		segment := data[pos+2 : pos+size]
		pos += size
		if marker != 0xe1 || !bytes.HasPrefix(segment, []byte("Exif")) {
			continue
		}
		if !bytes.HasPrefix(segment, []byte("Exif\x00\x00")) {
			return 0, fmt.Errorf("invalid JPEG EXIF header")
		}
		value, present, err := referenceEXIFOrientation(segment[6:])
		if err != nil {
			return 0, err
		}
		if present {
			if found && value != orientation {
				return 0, fmt.Errorf("conflicting JPEG EXIF orientations")
			}
			orientation, found = value, true
		}
	}
	return 0, fmt.Errorf("truncated JPEG headers")
}

func referenceEXIFOrientation(tiff []byte) (int, bool, error) {
	invalid := func() (int, bool, error) { return 0, false, fmt.Errorf("invalid JPEG EXIF orientation") }
	if len(tiff) < 8 {
		return invalid()
	}
	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return invalid()
	}
	if order.Uint16(tiff[2:4]) != 42 {
		return invalid()
	}
	offset := uint64(order.Uint32(tiff[4:8]))
	if offset < 8 || offset+2 > uint64(len(tiff)) {
		return invalid()
	}
	count := uint64(order.Uint16(tiff[offset : offset+2]))
	if offset+2+12*count+4 > uint64(len(tiff)) {
		return invalid()
	}
	// Orientation 只读取 IFD0 的 SHORT 单值；缩略图自己的方向不能覆盖主图。
	orientation, found := 1, false
	for i := uint64(0); i < count; i++ {
		entry := tiff[offset+2+12*i : offset+2+12*(i+1)]
		if order.Uint16(entry[:2]) != 0x0112 {
			continue
		}
		if found || order.Uint16(entry[2:4]) != 3 || order.Uint32(entry[4:8]) != 1 {
			return invalid()
		}
		orientation = int(order.Uint16(entry[8:10]))
		if orientation < 1 || orientation > 8 {
			return invalid()
		}
		found = true
	}
	return orientation, found, nil
}

func referenceOrientedDimensions(w, h, orientation int) (int, int) {
	if orientation >= 5 && orientation <= 8 {
		return h, w
	}
	return w, h
}

func decodeOrientedReferenceImage(data []byte) (image.Image, error) {
	orientation, err := referenceImageOrientation(data)
	if err != nil {
		return nil, err
	}
	source, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if orientation == 1 {
		return source, nil
	}
	bounds := source.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	dw, dh := referenceOrientedDimensions(w, h, orientation)
	result := image.NewNRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dx, dy := x, y
			switch orientation {
			case 2:
				dx = w - 1 - x
			case 3:
				dx, dy = w-1-x, h-1-y
			case 4:
				dy = h - 1 - y
			case 5:
				dx, dy = y, x
			case 6:
				dx, dy = h-1-y, x
			case 7:
				dx, dy = h-1-y, w-1-x
			case 8:
				dx, dy = y, w-1-x
			}
			result.Set(dx, dy, source.At(bounds.Min.X+x, bounds.Min.Y+y))
		}
	}
	return result, nil
}
