import { describe, expect, it } from "vitest";
import { capturePageGroups, capturePagePosition } from "./capturePageGrouping";

describe("同次选择跨文件连续分组", () => {
  it("多页文件不会各自重新开始分组，按最终文件顺序显示每一份来源", () => {
    const first = { name: "前3页.pdf" } as File, second = { name: "后3页.tiff" } as File;
    const groups = capturePageGroups([first, second], new Map([[first, 3], [second, 3]]), 2)!;
    expect(groups.map(group => group.received)).toEqual([2, 2, 2]);
    expect(groups[1].sources).toEqual([{ file: first, first: 3, last: 3 }, { file: second, first: 1, last: 1 }]);
    expect(capturePagePosition(3, 2)).toBe("第 2 份答卷，第 2 页");
    expect(capturePageGroups([second, first], new Map([[first, 3], [second, 3]]), 2)![0].sources[0].file).toBe(second);
  });
  it("未知页数不猜测分组，不完整末份保留已收页数", () => {
    const file = {} as File;
    expect(capturePageGroups([file], new Map(), 2)).toBeUndefined();
    expect(capturePageGroups([file], new Map([[file, 3]]), 2)![1].received).toBe(1);
  });
});
