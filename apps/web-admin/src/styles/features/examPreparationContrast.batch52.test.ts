import { readFileSync } from "node:fs";
import postcss from "postcss";
import { describe, expect, it } from "vitest";

const stylesheet = postcss.parse(readFileSync(new URL("./exam-preparation.css", import.meta.url), "utf8"));

function declarations(selector: string) {
  const values = new Map<string, string>();
  stylesheet.walkRules(selector, rule => {
    rule.walkDecls(declaration => { values.set(declaration.prop, declaration.value); });
  });
  return values;
}

function opaqueRgb(value: string) {
  const channels = /^#([0-9a-f]{3}|[0-9a-f]{6})$/i.exec(value)?.[1];
  if (!channels) throw new Error(`Expected an opaque hex color: ${value}`);
  const full = channels.length === 3 ? [...channels].map(channel => channel.repeat(2)).join("") : channels;
  return [0, 2, 4].map(offset => Number.parseInt(full.slice(offset, offset + 2), 16));
}

function luminance(channels: readonly number[]) {
  const linear = channels.map(channel => {
    const value = channel / 255;
    return value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * linear[0] + 0.7152 * linear[1] + 0.0722 * linear[2];
}

describe("扫描选项标签对比度", () => {
  it("标签使用不透明前景和背景，达到小字号文字的最低对比度", () => {
    // 直接读取交付样式；透明扫描图像不参与标签底色，不能只对着白纸验收。
    const label = declarations(".template-option-region > span");
    const text = luminance(opaqueRgb(label.get("color")!));
    const background = luminance(opaqueRgb(label.get("background")!));
    const ratio = (Math.max(text, background) + 0.05) / (Math.min(text, background) + 0.05);
    expect(ratio).toBeGreaterThanOrEqual(4.5);
    expect(label.get("max-width")).toBe("100%");
    expect(label.get("min-width")).toBe("1.2em");
    expect(label.get("box-sizing")).toBe("border-box");
    expect(label.get("overflow")).toBe("hidden");
    expect(label.get("text-overflow")).toBe("ellipsis");
  });

  it("选项框保持原件可见和指针穿透，实色底只用于嵌套标签", () => {
    const marker = declarations(".template-option-region");
    expect(marker.get("background")).toBe("transparent");
    expect(marker.get("pointer-events")).toBe("none");
    expect(marker.get("position")).toBe("absolute");
    expect(marker.get("border")).toBe("1px solid rgba(22, 119, 255, .5)");
    expect(declarations(".template-region > span").get("position")).toBe("absolute");
    expect(declarations(".template-option-region > span").has("position")).toBe(false);
  });
});
