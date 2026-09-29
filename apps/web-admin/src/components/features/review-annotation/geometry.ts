import type { CanonicalImageGeometry } from "@edugrade/sdk";

export interface ClientPoint {
  x: number;
  y: number;
}

export interface ClientBounds {
  left: number;
  top: number;
  width: number;
  height: number;
}

const clamp01 = (value: number) => Math.min(1, Math.max(0, value));

export function clientPointToCanonical(point: ClientPoint, bounds: ClientBounds): ClientPoint {
  // 输入是浏览器视口像素，输出是原图左上角起算的 0～1 比例；bounds 必须对应实际图像区域。
  if (bounds.width <= 0 || bounds.height <= 0) {
    return { x: 0, y: 0 };
  }
  return {
    x: clamp01((point.x - bounds.left) / bounds.width),
    y: clamp01((point.y - bounds.top) / bounds.height)
  };
}

export function canonicalRectangle(start: ClientPoint, end: ClientPoint): CanonicalImageGeometry {
  const x = clamp01(Math.min(start.x, end.x));
  const y = clamp01(Math.min(start.y, end.y));
  return {
    coordinate_space: "canonical_image_normalized",
    x,
    y,
    width: Math.min(1 - x, Math.abs(clamp01(end.x) - clamp01(start.x))),
    height: Math.min(1 - y, Math.abs(clamp01(end.y) - clamp01(start.y)))
  };
}

export function canonicalPoint(point: ClientPoint): CanonicalImageGeometry {
  return {
    coordinate_space: "canonical_image_normalized",
    x: clamp01(point.x),
    y: clamp01(point.y),
    width: 0,
    height: 0
  };
}

export function geometryStyle(geometry: CanonicalImageGeometry) {
  return {
    left: `${geometry.x * 100}%`,
    top: `${geometry.y * 100}%`,
    width: `${geometry.width * 100}%`,
    height: `${geometry.height * 100}%`
  };
}

export function hasDrawableArea(geometry: CanonicalImageGeometry, minimum = 0.003) {
  // 最小边长按图像比例计算，0.003 为宽高各 0.3%，不是固定屏幕像素。
  return geometry.width >= minimum && geometry.height >= minimum;
}
