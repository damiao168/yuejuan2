import { describe, expect, it } from "vitest";
import { detectAnswerSheetFrames } from "./answerSheetImageRegions";

function sheet(width = 600, height = 900) {
  return { width, height, data: new Uint8ClampedArray(width * height * 4).fill(255) };
}
type Sheet = ReturnType<typeof sheet>;
function pixel(image: Sheet, x: number, y: number, shade = 0) {
  if (x < 0 || y < 0 || x >= image.width || y >= image.height) return;
  const offset = (Math.round(y) * image.width + Math.round(x)) * 4;
  image.data[offset] = shade; image.data[offset + 1] = shade; image.data[offset + 2] = shade;
}
function horizontal(image: Sheet, x1: number, x2: number, y: number) {
  for (let x = x1; x <= x2; x += 1) pixel(image, x, y);
}
function vertical(image: Sheet, x: number, y1: number, y2: number) {
  for (let y = y1; y <= y2; y += 1) pixel(image, x, y);
}
function frame(image: Sheet, x: number, y: number, width: number, height: number, thickness = 1) {
  for (let offset = 0; offset < thickness; offset += 1) {
    horizontal(image, x, x + width, y + offset);
    horizontal(image, x, x + width, y + height + offset);
    vertical(image, x + offset, y, y + height);
    vertical(image, x + width + offset, y, y + height);
  }
}
function handwriting(image: Sheet, x: number, y: number, width: number, height: number) {
  // 连续弯折笔画与零散字形用于干扰边线提取；这是合成测试，不代表真实扫描识别率。
  for (let row = 0; row < height; row += 27) {
    for (let along = 0; along < width; along += 1) {
      const py = y + row + Math.round(Math.sin(along / 9) * 8);
      if (Math.floor(along / 19) % 4 !== 2) {
        pixel(image, x + along, py);
        pixel(image, x + along, py + 1);
      }
    }
  }
}
function expectGeometry(actual: { x: number; y: number; width: number; height: number }, image: Sheet, expected: number[]) {
  expect(actual.x).toBeCloseTo(expected[0] / image.width, 2);
  expect(actual.y).toBeCloseTo(expected[1] / image.height, 2);
  expect(actual.width).toBeCloseTo(expected[2] / image.width, 2);
  expect(actual.height).toBeCloseTo(expected[3] / image.height, 2);
}

describe("answer sheet printed frame detection", () => {
  it("keeps separate columns and horizontal divisions despite handwriting", () => {
    const image = sheet();
    frame(image, 30, 300, 250, 500);
    horizontal(image, 30, 280, 510);
    frame(image, 320, 300, 250, 500);
    horizontal(image, 320, 570, 620);
    handwriting(image, 45, 330, 220, 440);
    handwriting(image, 335, 340, 220, 410);
    const result = detectAnswerSheetFrames(image);
    expect(result).toHaveLength(4);
    [[30, 300, 250, 210], [320, 300, 250, 320], [30, 510, 250, 290], [320, 620, 250, 180]]
      .forEach((expected, index) => expectGeometry(result[index], image, expected));
    expect(result.every((item) => item.confidence >= .9 && item.confidence <= 1)).toBe(true);
  });

  it("finds shared full-width row edges without returning a parent frame across columns", () => {
    const image = sheet();
    frame(image, 30, 300, 540, 500);
    vertical(image, 295, 300, 800);
    horizontal(image, 30, 570, 550);
    const result = detectAnswerSheetFrames(image);
    expect(result).toHaveLength(4);
    expect(result.every((item) => item.width < .5)).toBe(true);
    expect(result.filter((item) => item.y < .5)).toHaveLength(2);
  });

  it("excludes page outline, shallow identity header and tiny registration cells", () => {
    const image = sheet();
    frame(image, 3, 3, 594, 894);
    frame(image, 40, 70, 220, 40);
    for (let x = 40; x < 220; x += 15) frame(image, x, 135, 12, 12);
    frame(image, 35, 320, 530, 450);
    const result = detectAnswerSheetFrames(image);
    expect(result).toHaveLength(1);
    expectGeometry(result[0], image, [35, 320, 530, 450]);
  });

  it("bridges a short scan dropout, lowers confidence and refuses a missing whole side", () => {
    const image = sheet();
    frame(image, 40, 320, 240, 420);
    frame(image, 320, 320, 240, 420);
    for (let y = 500; y < 508; y += 1) pixel(image, 40, y, 255);
    for (let y = 321; y < 740; y += 1) pixel(image, 560, y, 255);
    const result = detectAnswerSheetFrames(image);
    expect(result).toHaveLength(1);
    expectGeometry(result[0], image, [40, 320, 240, 420]);
    expect(result[0].confidence).toBeLessThan(.95);
    expect(result[0].confidence).toBeGreaterThan(.9);
  });

  it("returns no fabricated question-sized regions for blank pages or handwriting alone", () => {
    expect(detectAnswerSheetFrames(sheet())).toEqual([]);
    const image = sheet();
    handwriting(image, 30, 300, 530, 500);
    expect(detectAnswerSheetFrames(image)).toEqual([]);
  });

  it("handles thick and gray printed lines with normalized coordinates", () => {
    const image = sheet();
    frame(image, 35, 310, 530, 450, 3);
    for (let offset = 0; offset < image.data.length; offset += 4) {
      if (image.data[offset] === 0) {
        image.data[offset] = 135; image.data[offset + 1] = 135; image.data[offset + 2] = 135;
      }
    }
    const result = detectAnswerSheetFrames(image);
    expect(result).toHaveLength(1);
    expectGeometry(result[0], image, [35, 310, 530, 450]);
    expect(result[0].confidence).toBeGreaterThan(.9);
  });

  it("does not emit duplicate inner and outer edges of a bold printed frame", () => {
    const image = sheet();
    frame(image, 35, 310, 530, 450, 8);
    const result = detectAnswerSheetFrames(image);
    expect(result).toHaveLength(1);
    expectGeometry(result[0], image, [38.5, 313.5, 530, 450]);
  });

  it("composites transparent pixels onto white rather than recognizing invisible black borders", () => {
    const image = sheet();
    frame(image, 35, 310, 530, 450);
    for (let offset = 3; offset < image.data.length; offset += 4) image.data[offset] = 0;
    expect(detectAnswerSheetFrames(image)).toEqual([]);
  });

  it("preserves one-pixel borders when reducing a large uploaded image", () => {
    const image = sheet(1800, 2400);
    frame(image, 120, 800, 1500, 1200);
    const result = detectAnswerSheetFrames(image);
    expect(result).toHaveLength(1);
    expectGeometry(result[0], image, [120, 800, 1500, 1200]);
  });

  it("rejects malformed or unbounded dimensions before allocating the working raster", () => {
    const data = new Uint8ClampedArray(4);
    for (const [width, height] of [[Infinity, 100], [NaN, 100], [-1, 100], [1.5, 100], [16001, 100], [12000, 12000], [600, 900]]) {
      expect(detectAnswerSheetFrames({ width, height, data })).toEqual([]);
    }
  });

  it("does not treat a dense registration grid as hundreds of answer frames", () => {
    const image = sheet();
    for (let x = 10; x < 590; x += 8) vertical(image, x, 10, 890);
    for (let y = 10; y < 890; y += 8) horizontal(image, 10, 590, y);
    expect(detectAnswerSheetFrames(image)).toEqual([]);
  });
});
