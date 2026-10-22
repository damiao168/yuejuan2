export type AnswerSheetFrame = { x: number; y: number; width: number; height: number; confidence: number };
type SheetPixels = { width: number; height: number; data: Uint8ClampedArray };
type Line = { at: number; start: number; end: number; weight: number };

const MAX_PIXELS = 1_600_000;
const MAX_SOURCE_PIXELS = 100_000_000;

// 先限制工作栅格，再按长直线闭合找框；手写内容不参与题号或题目数量的推断。
// 这里只给轴对齐的边框候选。倾斜、透视或缺失整条边时宁可不建议，由调用方提示复核。
function rasterize(image: SheetPixels) {
  const { width, height, data } = image;
  if (!Number.isSafeInteger(width) || !Number.isSafeInteger(height) || width < 32 || height < 32
    || width > 16_000 || height > 16_000 || width * height > MAX_SOURCE_PIXELS
    || data.length < width * height * 4) return undefined;
  const scale = Math.min(1, 1280 / width, 1600 / height, Math.sqrt(MAX_PIXELS / (width * height)));
  const w = Math.max(1, Math.floor(width * scale));
  const h = Math.max(1, Math.floor(height * scale));
  const gray = new Uint8Array(w * h);
  const histogram = new Uint32Array(256);
  for (let y = 0; y < h; y += 1) {
    for (let x = 0; x < w; x += 1) {
      let value = 255;
      // 缩小时取格内有限个位置的暗值，避免一像素印刷边线刚好落在采样点之间。
      const samples = scale < 1 ? 3 : 1;
      for (let sy = 0; sy < samples; sy += 1) {
        const py = Math.min(height - 1, Math.floor((y + (sy + .5) / samples) * height / h));
        for (let sx = 0; sx < samples; sx += 1) {
          const px = Math.min(width - 1, Math.floor((x + (sx + .5) / samples) * width / w));
          const offset = (py * width + px) * 4;
          const alpha = data[offset + 3] / 255;
          const luminance = (data[offset] * 2 + data[offset + 1] * 5 + data[offset + 2]) / 8;
          value = Math.min(value, Math.round(255 - (255 - luminance) * alpha));
        }
      }
      gray[y * w + x] = value;
      histogram[value] += 1;
    }
  }
  let population = 0;
  let background = 255;
  for (let value = 0; value < 256; value += 1) {
    population += histogram[value];
    if (population >= gray.length * .9) { background = value; break; }
  }
  const threshold = Math.max(45, Math.min(190, background - 55));
  for (let i = 0; i < gray.length; i += 1) gray[i] = gray[i] < threshold ? 1 : 0;
  return { pixels: gray, width: w, height: h };
}

function findLines(pixels: Uint8Array, width: number, height: number, vertical: boolean): Line[] {
  const extent = vertical ? height : width;
  const tracks = vertical ? width : height;
  const minimum = Math.max(vertical ? 24 : 36, Math.round(extent * (vertical ? .045 : .09)));
  const lines: Line[] = [];
  for (let track = 0; track < tracks; track += 1) {
    let start = -1;
    let lastInk = -1;
    let ink = 0;
    const finish = () => {
      if (start >= 0 && lastInk - start + 1 >= minimum && ink / (lastInk - start + 1) >= .75) {
        const previous = lines.slice(-32).find((line) => track - line.at <= 3
          && Math.abs(line.start - start) <= 5 && Math.abs(line.end - lastInk) <= 5);
        if (previous) {
          previous.at = (previous.at * previous.weight + track) / (previous.weight + 1);
          previous.weight += 1;
          previous.start = Math.min(previous.start, start);
          previous.end = Math.max(previous.end, lastInk);
        } else lines.push({ at: track, start, end: lastInk, weight: 1 });
      }
      start = -1; lastInk = -1; ink = 0;
    };
    for (let along = 0; along <= extent; along += 1) {
      const dark = along < extent && pixels[vertical ? along * width + track : track * width + along];
      if (dark) {
        if (start < 0) start = along;
        lastInk = along;
        ink += 1;
      } else if (start >= 0 && along - lastInk > 2) finish();
    }
    finish();
    // 极密表格或噪声不应让候选组合耗尽浏览器内存。
    if (lines.length > 4096) return [];
  }
  const merged: Line[] = [];
  const maxGap = Math.max(6, Math.round(extent * .012));
  const lineBand = Math.max(2, Math.min(6, Math.round(tracks * .01)));
  for (const line of lines.sort((a, b) => a.at - b.at || a.start - b.start)) {
    const previous = merged.find((candidate) => Math.abs(candidate.at - line.at) <= lineBand
      && line.start <= candidate.end + maxGap && line.end >= candidate.start - maxGap);
    if (previous) {
      previous.at = (previous.at * previous.weight + line.at * line.weight) / (previous.weight + line.weight);
      previous.weight += line.weight;
      previous.start = Math.min(previous.start, line.start);
      previous.end = Math.max(previous.end, line.end);
    } else merged.push({ ...line });
  }
  return merged.sort((a, b) => (b.end - b.start) - (a.end - a.start)).slice(0, vertical ? 128 : 256);
}

function edgeCoverage(pixels: Uint8Array, width: number, height: number, at: number, start: number, end: number, vertical: boolean) {
  let covered = 0;
  let total = 0;
  for (let along = Math.round(start); along <= Math.round(end); along += 1) {
    total += 1;
    for (let offset = -2; offset <= 2; offset += 1) {
      const x = vertical ? Math.round(at) + offset : along;
      const y = vertical ? along : Math.round(at) + offset;
      if (x >= 0 && x < width && y >= 0 && y < height && pixels[y * width + x]) {
        covered += 1;
        break;
      }
    }
  }
  return total ? covered / total : 0;
}

function contains(outer: AnswerSheetFrame, inner: AnswerSheetFrame) {
  return inner.x >= outer.x - .004 && inner.y >= outer.y - .004
    && inner.x + inner.width <= outer.x + outer.width + .004
    && inner.y + inner.height <= outer.y + outer.height + .004;
}

function sameFrame(a: AnswerSheetFrame, b: AnswerSheetFrame) {
  const overlap = Math.max(0, Math.min(a.x + a.width, b.x + b.width) - Math.max(a.x, b.x))
    * Math.max(0, Math.min(a.y + a.height, b.y + b.height) - Math.max(a.y, b.y));
  return overlap / (a.width * a.height + b.width * b.height - overlap) > .9;
}

/**
 * detectAnswerSheetFrames 返回以左上角为原点、0～1 页面比例的候选边框，粗线取线带中心。
 * confidence 只衡量四边直线证据，不表示题号匹配正确；调用方必须用题号证据绑定题目。
 * 几何规则只能排除浅身份栏与小格，无法理解所有表头，不能据此直接确认模板。
 */
export function detectAnswerSheetFrames(image: SheetPixels): AnswerSheetFrame[] {
  const raster = rasterize(image);
  if (!raster) return [];
  const { pixels, width, height } = raster;
  const vertical = findLines(pixels, width, height, true).sort((a, b) => a.at - b.at);
  const horizontal = findLines(pixels, width, height, false).sort((a, b) => a.at - b.at);
  const candidates: AnswerSheetFrame[] = [];
  for (let leftIndex = 0; leftIndex < vertical.length; leftIndex += 1) {
    const left = vertical[leftIndex];
    for (let rightIndex = leftIndex + 1; rightIndex < vertical.length; rightIndex += 1) {
      const right = vertical[rightIndex];
      const boxWidth = right.at - left.at;
      if (boxWidth < Math.max(40, width * .10)) continue;
      const topLimit = Math.max(left.start, right.start);
      const bottomLimit = Math.min(left.end, right.end);
      if (bottomLimit - topLimit < Math.max(20, height * .025)) continue;
      const bounds = horizontal.filter((line) => line.start <= left.at + 4 && line.end >= right.at - 4
        && line.at >= topLimit - 4 && line.at <= bottomLimit + 4);
      // 同一对竖边内有横线时按相邻横边分格，避免把整列大框覆盖到多个题目上。
      for (let index = 1; index < bounds.length; index += 1) {
        const top = bounds[index - 1].at;
        const bottom = bounds[index].at;
        const frame = { x: left.at / width, y: top / height, width: boxWidth / width, height: (bottom - top) / height, confidence: 0 };
        if (bottom - top < Math.max(20, height * .025) || frame.width * frame.height < .004
          || (frame.width > .88 && frame.height > .85)
          || (frame.y < .27 && frame.height < .095)) continue;
        const edges = [
          edgeCoverage(pixels, width, height, top, left.at, right.at, false),
          edgeCoverage(pixels, width, height, bottom, left.at, right.at, false),
          edgeCoverage(pixels, width, height, left.at, top, bottom, true),
          edgeCoverage(pixels, width, height, right.at, top, bottom, true)
        ];
        if (edges.some((coverage) => coverage < .78)) continue;
        frame.confidence = Math.round((.55 + .4 * edges.reduce((sum, coverage) => sum + coverage, 0) / 4) * 1000) / 1000;
        candidates.push(frame);
        if (candidates.length >= 1024) break;
      }
      if (candidates.length >= 1024) break;
    }
    if (candidates.length >= 1024) break;
  }
  // 粗线可形成位置稍有差异的内外边缘；按面积重叠消重，不能只比较浮点坐标是否相等。
  const distinct = candidates.sort((a, b) => b.confidence - a.confidence).filter((frame, index, all) =>
    !all.slice(0, index).some((other) => sameFrame(other, frame)));
  return distinct.filter((frame) => {
    const area = frame.width * frame.height;
    const children = distinct.filter((other) => other !== frame && contains(frame, other)
      && other.width * other.height < area * .92 && other.width * other.height > area * .08);
    return !(children.length >= 2 && children.reduce((sum, child) => sum + child.width * child.height, 0) > area * .4);
  }).slice(0, 128).sort((a, b) => a.y - b.y || a.x - b.x);
}
