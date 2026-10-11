package scorerelease

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/paper"
)

func TestAnonymousPaperRedactionRemovesIdentityPixelsAndMetadata(t *testing.T) {
	source := syntheticAnonymousIdentityPage()
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	// 解码器可能忽略尾部身份标记；重新编码器仍必须省略解码像素矩阵之外的全部来源字节。
	encoded.WriteString("synthetic-name-and-student-number-SECRET-QR")
	page := anonymousSourcePage{pageNo: 1, layout: paper.TemplateLayout{Pages: []paper.TemplatePage{{
		PageNo: 1, Width: 1000, Height: 1400,
		IdentityRegions: []paper.LayoutRegion{{X: 0, Y: 0, Width: 0.5, Height: 0.2}},
	}}}}
	if !validAnonymousLayout(anonymousSourcePage{pageNo: 1, size: int64(encoded.Len()), sourceHash: strings.Repeat("a", 64), contentType: "image/png", templateID: "t", templateHash: "h", layout: page.layout}) {
		t.Fatal("valid synthetic identity region rejected")
	}
	derived, err := redactAnonymousPNG(encoded.Bytes(), page)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(derived, []byte("SECRET-QR")) || bytes.Equal(derived, encoded.Bytes()) {
		t.Fatal("derived image retained source metadata or source bytes")
	}
	decoded, err := png.Decode(bytes.NewReader(derived))
	if err != nil {
		t.Fatal(err)
	}
	assertAnonymousIdentityPixelsCovered(t, decoded)
	if decoded.At(80, 100) != source.At(80, 100) {
		t.Fatal("non-identity answer region changed")
	}
	page.layout.Pages[0].IdentityRegions = nil
	if validAnonymousLayout(anonymousSourcePage{pageNo: 1, size: int64(encoded.Len()), sourceHash: strings.Repeat("a", 64), contentType: "image/png", templateID: "t", templateHash: "h", layout: page.layout}) {
		t.Fatal("missing identity region must block sharing")
	}
}

func syntheticAnonymousIdentityPage() *image.RGBA {
	source := image.NewRGBA(image.Rect(0, 0, 100, 140))
	for y := 0; y < 140; y++ {
		for x := 0; x < 100; x++ {
			source.Set(x, y, color.RGBA{R: 80, G: 160, B: 210, A: 255})
		}
	}
	// 合成姓名和学号用红色笔画表示；黑白交替方块代表二维码。
	for y := 3; y < 24; y++ {
		for x := 3; x < 48; x++ {
			if x < 25 || (x/3+y/3)%2 == 0 {
				source.Set(x, y, color.RGBA{R: 220, G: 10, B: 10, A: 255})
			} else {
				source.Set(x, y, color.Black)
			}
		}
	}
	return source
}

func assertAnonymousIdentityPixelsCovered(t *testing.T, decoded image.Image) {
	t.Helper()
	for _, point := range []image.Point{{5, 5}, {25, 12}, {45, 20}} {
		r, g, b, a := decoded.At(point.X, point.Y).RGBA()
		if r != 0xffff || g != 0xffff || b != 0xffff || a != 0xffff {
			t.Fatalf("identity pixel at %v was not irreversibly covered: %v", point, decoded.At(point.X, point.Y))
		}
	}
}
