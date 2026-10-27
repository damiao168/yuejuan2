// 裁片使用原页像素坐标并保留现有边界与 PNG 编码，独立于上传和数据库绑定。
package main

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"math"
	"os"
)

func decodeImage(path string) (image.Image, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	decoded, _, err := image.Decode(file)
	return decoded, err
}

func cropAnswerRegion(source image.Image, item region, padding int) ([]byte, image.Rectangle, error) {
	if source == nil {
		return nil, image.Rectangle{}, errors.New("source image is nil")
	}
	if item.Width <= 0 || item.Height <= 0 {
		return nil, image.Rectangle{}, errors.New("crop region must have positive width and height")
	}
	if padding < 0 {
		return nil, image.Rectangle{}, errors.New("crop padding cannot be negative")
	}
	bounds := source.Bounds()
	left := bounds.Min.X + int(math.Floor(item.X*float64(bounds.Dx()))) - padding
	top := bounds.Min.Y + int(math.Floor(item.Y*float64(bounds.Dy()))) - padding
	right := bounds.Min.X + int(math.Ceil((item.X+item.Width)*float64(bounds.Dx()))) + padding
	bottom := bounds.Min.Y + int(math.Ceil((item.Y+item.Height)*float64(bounds.Dy()))) + padding
	left = max(left, bounds.Min.X)
	top = max(top, bounds.Min.Y)
	right = min(right, bounds.Max.X)
	bottom = min(bottom, bounds.Max.Y)
	cropBounds := image.Rect(left, top, right, bottom)
	if cropBounds.Empty() {
		return nil, image.Rectangle{}, fmt.Errorf("crop region is outside image bounds %v", bounds)
	}
	cropped := image.NewRGBA(image.Rect(0, 0, cropBounds.Dx(), cropBounds.Dy()))
	draw.Draw(cropped, cropped.Bounds(), source, cropBounds.Min, draw.Src)
	var output bytes.Buffer
	if err := png.Encode(&output, cropped); err != nil {
		return nil, image.Rectangle{}, err
	}
	return output.Bytes(), cropBounds, nil
}
