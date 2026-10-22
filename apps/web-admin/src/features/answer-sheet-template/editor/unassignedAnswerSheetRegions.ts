import type { LayoutRegion } from "../../../api/configuration";
import type { SheetFrame } from "./answerSheetRegionMatching";

export type UnassignedRegions = Map<number, LayoutRegion[]>;

function intersection(left: LayoutRegion | SheetFrame, right: LayoutRegion | SheetFrame) {
  const x = Math.max(left.x, right.x), y = Math.max(left.y, right.y);
  return { x, y, width: Math.max(0, Math.min(left.x + left.width, right.x + right.width) - x),
    height: Math.max(0, Math.min(left.y + left.height, right.y + right.height) - y) };
}

function sameFrame(left: LayoutRegion, right: LayoutRegion) {
  const overlap = intersection(left, right);
  const area = overlap.width * overlap.height;
  return area / (left.width * left.height + right.width * right.height - area) >= .94;
}

function coveredByRegions(frame: LayoutRegion | SheetFrame, regions: LayoutRegion[]) {
  const overlaps = regions.map(region => intersection(frame, region)).filter(rect => rect.width > 0 && rect.height > 0);
  const edges = [...new Set(overlaps.flatMap(rect => [rect.x, rect.x + rect.width]))].sort((a, b) => a - b);
  let area = 0;
  for (let index = 1; index < edges.length; index++) {
    const left = edges[index - 1], right = edges[index];
    const intervals = overlaps.filter(rect => rect.x < right && rect.x + rect.width > left)
      .map(rect => [rect.y, rect.y + rect.height]).sort((a, b) => a[0] - b[0]);
    let bottom = -Infinity;
    for (const [top, end] of intervals) {
      if (end > bottom) area += (right - left) * (end - Math.max(top, bottom));
      bottom = Math.max(bottom, end);
    }
  }
  // 同一大框可由多个选择题小区域共同覆盖；不能把这些已配置范围再次显示为待关联框。
  return area >= frame.width * frame.height * .9;
}

/** 未配对框独立于服务端 layout：关联题目以前不能保存为 question_regions。 */
export function collectUnassignedFrames(frames: SheetFrame[], regions: LayoutRegion[]): LayoutRegion[] {
  return frames.filter(frame => !coveredByRegions(frame, regions)).map(frame => ({
    id: crypto.randomUUID(), label: "待关联题目", x: frame.x, y: frame.y,
    width: frame.width, height: frame.height, option_regions: [],
    suggestion_source: "ocr_layout" as const, suggestion_confidence: frame.confidence
  }));
}

/** 重试按页和几何合并，保留失败页旧候选；没有 question_id 的不同框不能互相覆盖。 */
export function mergeUnassignedFrames(previous: UnassignedRegions, incoming: UnassignedRegions, covered: UnassignedRegions) {
  const output: UnassignedRegions = new Map();
  for (const pageNo of new Set([...previous.keys(), ...incoming.keys()])) {
    const regions = [...(previous.get(pageNo) ?? [])];
    for (const candidate of incoming.get(pageNo) ?? []) {
      if (!regions.some(region => sameFrame(region, candidate))) regions.push(candidate);
    }
    const remaining = regions.filter(region => !coveredByRegions(region, covered.get(pageNo) ?? []));
    if (remaining.length) output.set(pageNo, remaining);
  }
  return output;
}
