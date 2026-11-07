package main

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
)

func syntheticOMRImages() ([]byte, []byte, error) {
	base := image.NewRGBA(image.Rect(0, 0, 160, 70))
	draw.Draw(base, base.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	for _, x := range []int{20, 70, 120} {
		drawOutline(base, image.Rect(x, 20, x+28, 48), color.Black, 2)
	}
	// 此处模拟选项 A 内的印刷表单内容。原始填涂比例 OMR 会把它当作第二个标记；
	// 模板差分 worker 必须先按冻结空白参考去除它，再选择 B。
	draw.Draw(base, image.Rect(27, 27, 41, 41), &image.Uniform{C: color.Black}, image.Point{}, draw.Src)
	template, err := encodePNG(base)
	if err != nil {
		return nil, nil, err
	}
	answer := image.NewRGBA(base.Bounds())
	draw.Draw(answer, answer.Bounds(), base, image.Point{}, draw.Src)
	fillEllipse(answer, 76, 26, 92, 42, color.Black)
	marked, err := encodePNG(answer)
	if err != nil {
		return nil, nil, err
	}
	return template, marked, nil
}

func drawOutline(dst draw.Image, rect image.Rectangle, colour color.Color, width int) {
	for offset := 0; offset < width; offset++ {
		for x := rect.Min.X + offset; x < rect.Max.X-offset; x++ {
			dst.Set(x, rect.Min.Y+offset, colour)
			dst.Set(x, rect.Max.Y-1-offset, colour)
		}
		for y := rect.Min.Y + offset; y < rect.Max.Y-offset; y++ {
			dst.Set(rect.Min.X+offset, y, colour)
			dst.Set(rect.Max.X-1-offset, y, colour)
		}
	}
}

func fillEllipse(dst draw.Image, minX, minY, maxX, maxY int, colour color.Color) {
	cx, cy := float64(minX+maxX-1)/2, float64(minY+maxY-1)/2
	rx, ry := float64(maxX-minX)/2, float64(maxY-minY)/2
	for y := minY; y < maxY; y++ {
		for x := minX; x < maxX; x++ {
			dx, dy := (float64(x)-cx)/rx, (float64(y)-cy)/ry
			if dx*dx+dy*dy <= 1 {
				dst.Set(x, y, colour)
			}
		}
	}
}

func encodePNG(img image.Image) ([]byte, error) {
	var output bytes.Buffer
	if err := png.Encode(&output, img); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
