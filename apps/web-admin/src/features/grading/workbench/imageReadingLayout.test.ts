import { describe, expect, it } from "vitest";
import { planImageReadingLayout, type ImageReadingLayout } from "./imageReadingLayout";

function expectCompleteCoverage(layout: ImageReadingLayout, sourceLength: number) {
  const vertical = layout.axis === "vertical";
  const start = (index: number) => vertical ? layout.tiles[index].y : layout.tiles[index].x;
  const length = vertical ? layout.tileHeight : layout.tileWidth;
  expect(start(0)).toBe(0);
  expect(start(layout.tiles.length - 1) + length).toBeCloseTo(sourceLength, 5);
  expect(new Set(layout.tiles.map((tile) => tile.index)).size).toBe(layout.tiles.length);
  layout.tiles.forEach((tile, index) => {
    expect(tile.width).toBe(layout.tileWidth);
    expect(tile.height).toBe(layout.tileHeight);
    if (index === 0) return;
    expect(start(index)).toBeGreaterThan(start(index - 1));
    expect(start(index)).toBeLessThan(start(index - 1) + length);
    const overlap = (start(index - 1) + length - start(index)) / length;
    expect(overlap).toBeGreaterThanOrEqual(0.1 - 1e-9);
    if (index < layout.tiles.length - 1) expect(overlap).toBeCloseTo(0.1, 8);
  });
}

describe("planImageReadingLayout", () => {
  it("纵长图一次横排所有分段，完整覆盖且整体放入画布", () => {
    const layout = planImageReadingLayout({ imageWidth: 600, imageHeight: 6000, viewportWidth: 1800, viewportHeight: 900 });
    expect(layout.axis).toBe("vertical");
    expect(layout.columns).toBe(layout.tiles.length);
    expect(layout.rows).toBe(1);
    expect(layout.scale).toBeGreaterThan((900 / 6000) * 3);
    expect(layout.displayWidth * layout.columns + layout.gap * (layout.columns - 1)).toBeLessThanOrEqual(1800);
    expect(layout.displayHeight).toBeLessThanOrEqual(900);
    expectCompleteCoverage(layout, 6000);
  });

  it("横长图沿原图横向分段并按画布高度竖排三行", () => {
    const layout = planImageReadingLayout({ imageWidth: 7000, imageHeight: 500, viewportWidth: 1200, viewportHeight: 900 });
    expect(layout.axis).toBe("horizontal");
    expect(layout.columns).toBe(1);
    expect(layout.rows).toBe(3);
    expect(layout.scale).toBeGreaterThan(1200 / 7000);
    expect(layout.rows).toBe(layout.tiles.length);
    expect(layout.displayHeight * 3 + layout.gap * 2).toBeLessThanOrEqual(900);
    expect(layout.displayWidth).toBeCloseTo(1200);
    expectCompleteCoverage(layout, 7000);
  });

  it("图片比例接近画布时保留整图，纵长图片在匹配的竖屏也无需分段", () => {
    const landscape = planImageReadingLayout({ imageWidth: 1600, imageHeight: 1000, viewportWidth: 1200, viewportHeight: 800 });
    const portrait = planImageReadingLayout({ imageWidth: 600, imageHeight: 2400, viewportWidth: 300, viewportHeight: 1200 });
    expect(landscape.axis).toBe("none");
    expect(landscape.tiles).toEqual([{ index: 0, x: 0, y: 0, width: 1600, height: 1000 }]);
    expect(landscape.scale).toBe(0.75);
    expect(portrait.axis).toBe("none");
    expect(portrait.tiles).toHaveLength(1);
    expect(portrait.scale).toBe(0.5);
  });

  it("缩窄窗口后重算完整分段，所有列仍留在同一画布", () => {
    const image = { imageWidth: 600, imageHeight: 6000 };
    const wide = planImageReadingLayout({ ...image, viewportWidth: 1800, viewportHeight: 900 });
    const narrow = planImageReadingLayout({ ...image, viewportWidth: 700, viewportHeight: 900 });
    expect(wide.columns).toBeGreaterThan(narrow.columns);
    expect(narrow.columns).toBe(narrow.tiles.length);
    expect(wide.tileHeight).not.toBe(narrow.tileHeight);
    expect(narrow.displayWidth * narrow.columns + narrow.gap * (narrow.columns - 1)).toBeCloseTo(700);
    expectCompleteCoverage(narrow, image.imageHeight);
  });

  it("一组分段不能比整图放大时保留整图，不制造第二组", () => {
    const layout = planImageReadingLayout({ imageWidth: 1000, imageHeight: 1000, viewportWidth: 1400, viewportHeight: 700 });
    expect(layout.axis).toBe("none");
    expect(layout.columns).toBe(1);
    expect(layout.scale).toBe(0.7);
    expect(layout.tiles).toEqual([{ index: 0, x: 0, y: 0, width: 1000, height: 1000 }]);
  });

  it("第18题按完整覆盖反推两段长度，末尾不再额外留一段到下一组", () => {
    const layout = planImageReadingLayout({ imageWidth: 1119, imageHeight: 2415, viewportWidth: 1520, viewportHeight: 760, captionHeight: 20, gap: 8 });
    expect(layout.tiles).toHaveLength(2);
    expect(layout.columns).toBe(2);
    expect(layout.displayHeight + 20).toBeCloseTo(760);
    expect(layout.displayWidth * 2 + 8).toBeLessThanOrEqual(1520);
    expectCompleteCoverage(layout, 2415);
  });

  it("极长图限制几何计划段数，同时仍覆盖原图最末端", () => {
    const layout = planImageReadingLayout({ imageWidth: 100, imageHeight: 1e12, viewportWidth: 1800, viewportHeight: 900 });
    expect(layout.tiles.length).toBeLessThanOrEqual(4096);
    expect(layout.columns).toBe(layout.tiles.length);
    expect(layout.displayWidth * layout.columns + layout.gap * (layout.columns - 1)).toBeLessThanOrEqual(1800);
    expect(layout.scale).toBeGreaterThan(0);
    expect(layout.displayHeight).toBeCloseTo(900);
    expectCompleteCoverage(layout, 1e12);
  });

  it.each([0, -1, Number.NaN, Number.POSITIVE_INFINITY])("无效图片尺寸 %s 返回空计划", (imageWidth) => {
    const layout = planImageReadingLayout({ imageWidth, imageHeight: 1000, viewportWidth: 800, viewportHeight: 600 });
    expect(layout.axis).toBe("none");
    expect(layout.tiles).toEqual([]);
    expect(layout.scale).toBe(1);
    expect(layout.displayWidth).toBe(0);
  });

  it("画布尚未测量时保留原图，非法间距回退默认值", () => {
    const layout = planImageReadingLayout({ imageWidth: 600, imageHeight: 6000, viewportWidth: 0, viewportHeight: Number.NaN, gap: -3 });
    expect(layout.axis).toBe("none");
    expect(layout.tiles).toEqual([{ index: 0, x: 0, y: 0, width: 600, height: 6000 }]);
    expect(layout.scale).toBe(1);
    expect(layout.gap).toBe(12);
  });

  it("过大间距下调到能放入画布的列数", () => {
    const layout = planImageReadingLayout({ imageWidth: 600, imageHeight: 6000, viewportWidth: 1800, viewportHeight: 900, gap: 2000 });
    expect(layout.columns).toBe(1);
    expect(layout.axis).toBe("none");
    expect(layout.tiles).toEqual([{ index: 0, x: 0, y: 0, width: 600, height: 6000 }]);
  });

  it("横长图每行标题都计入高度，所有分段无需翻组", () => {
    const layout = planImageReadingLayout({ imageWidth: 18000, imageHeight: 600, viewportWidth: 1800, viewportHeight: 900, captionHeight: 20, gap: 8 });
    expect(layout.axis).toBe("horizontal");
    expect(layout.rows).toBe(layout.tiles.length);
    expect(layout.displayHeight * layout.rows + 20 * layout.rows + 8 * (layout.rows - 1)).toBeLessThanOrEqual(900);
    expectCompleteCoverage(layout, 18000);
  });
});
