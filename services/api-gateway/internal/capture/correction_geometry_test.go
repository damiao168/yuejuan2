package capture

import "testing"

func TestValidateCorrectionPoints(t *testing.T) {
	// 覆盖同向四边形、反向点序、重复点、越界点和小面积退化输入。
	valid := []NormalizedPoint{{0, 0}, {1, 0}, {1, 1}, {0, 1}}
	if err := validateCorrectionPoints(valid, valid); err != nil {
		t.Fatal(err)
	}
	invalid := [][]NormalizedPoint{
		{{0, 0}, {1, 1}, {1, 0}, {0, 1}},
		{{0, 0}, {0, 0}, {1, 1}, {0, 1}},
		{{-0.1, 0}, {1, 0}, {1, 1}, {0, 1}},
		{{0, 0}, {.1, 0}, {.1, .1}, {0, .1}},
	}
	for _, points := range invalid {
		if err := validateCorrectionPoints(points, valid); err == nil {
			t.Fatalf("expected invalid points: %#v", points)
		}
	}
}
