package capture

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
)

func validateCorrectionPoints(source, target []NormalizedPoint) error {
	if !validNormalizedQuad(source) || !validNormalizedQuad(target) {
		return ErrInvalidInput
	}
	// 对应四角必须按相同方向排列，反向会把透视校正变成镜像翻转。
	if signedQuadArea(source)*signedQuadArea(target) <= 0 {
		return ErrInvalidInput
	}
	return nil
}

func validNormalizedQuad(points []NormalizedPoint) bool {
	if len(points) != 4 {
		return false
	}
	seen := map[[2]float64]bool{}
	var crossSign float64
	for i, point := range points {
		if math.IsNaN(point.X) || math.IsNaN(point.Y) || math.IsInf(point.X, 0) || math.IsInf(point.Y, 0) || point.X < 0 || point.X > 1 || point.Y < 0 || point.Y > 1 {
			return false
		}
		key := [2]float64{point.X, point.Y}
		if seen[key] {
			return false
		}
		seen[key] = true
		a, b, c := points[i], points[(i+1)%4], points[(i+2)%4]
		cross := (b.X-a.X)*(c.Y-b.Y) - (b.Y-a.Y)*(c.X-b.X)
		if math.Abs(cross) < 1e-9 || crossSign != 0 && cross*crossSign < 0 {
			return false
		}
		crossSign = cross
	}
	// 坐标归一到整幅图像；有效四边形至少覆盖 5% 面积，排除退化的小区域。
	return math.Abs(signedQuadArea(points)) >= 0.05
}

func signedQuadArea(points []NormalizedPoint) float64 {
	area := 0.0
	for i, point := range points {
		next := points[(i+1)%len(points)]
		area += point.X*next.Y - next.X*point.Y
	}
	return area / 2
}

func correctionPointsHash(source, target []NormalizedPoint) string {
	raw, _ := json.Marshal(map[string]any{"source": source, "template": target})
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}
