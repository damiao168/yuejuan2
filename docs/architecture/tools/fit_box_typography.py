"""放大原生文字，并按实际字宽收紧方框；原始几何保留以便重复生成。"""
from functools import lru_cache
from pathlib import Path
import json
import math

from PIL import ImageFont

BASE = Path(__file__).resolve().parents[1]
TITLE_MIN, TITLE_MAX, BODY_SIZE = 40, 72, 32
ENTITY_TITLE, ENTITY_FIELDS = 34, 26
PADDING = 12


@lru_cache(maxsize=None)
def font(size, bold=False):
    # 与 draw.io 使用同一中文字体，按真实字宽计算，避免英文标识符被低估。
    face = "msyhbd.ttc" if bold else "msyh.ttc"
    return ImageFont.truetype(str(Path("C:/Windows/Fonts") / face), size)


def text_width(text, size, bold=False):
    return font(size, bold).getlength(text)


def wrapped_rows(lines, size, width):
    rows = 0
    for line in lines:
        used, count = 0, 1
        for char in line:
            length = text_width(char, size)
            if used and used + length > width:
                count += 1
                used = 0
            used += length
        rows += count
    return rows


def scale_original_layout(page, scale):
    # 重跑时从首次排版前的位置开始；不累乘缩放，不改标签、状态或出处。
    if "typography_layout_base" not in page:
        page["typography_layout_base"] = {
            "width": page.get("width", 1800), "height": page.get("height", 1250),
            "nodes": {n["id"]: {k: n[k] for k in ("x", "y", "w", "h")} for n in page["nodes"]},
            "points": {str(i): edge["points"] for i, edge in enumerate(page.get("edges", [])) if edge.get("points")},
        }
    base = page["typography_layout_base"]
    page["width"], page["height"] = round(base["width"] * scale), round(base["height"] * scale)
    for i, edge in enumerate(page.get("edges", [])):
        if str(i) in base["points"]:
            edge["points"] = [[round(x * scale), round(y * scale)] for x, y in base["points"][str(i)]]
    return base


def fitted_text_width(lines, width, entity, inner_ratio):
    available = (width - 2 * PADDING) * inner_ratio
    if entity:
        title_size, body_size = ENTITY_TITLE, ENTITY_FIELDS
        fitted_width = max(width, text_width(lines[0], title_size, True) + 2 * PADDING)
    else:
        unit = max(1, text_width(lines[0], TITLE_MIN, True))
        title_size = max(TITLE_MIN, min(TITLE_MAX, math.floor(TITLE_MIN * available / unit)))
        body_size = BODY_SIZE
        title_width = text_width(lines[0], title_size, True)
        body_width = min(available, max((text_width(s, body_size) for s in lines[1:]), default=0))
        fitted_width = max(title_width, body_width) / inner_ratio + 2 * PADDING
    return title_size, body_size, fitted_width


def fit_node(node, old, scale):
    x, y, width, height = [old[k] * scale for k in ("x", "y", "w", "h")]
    lines = node.get("display_label", node["label"]).split("\n")
    kind = node.get("kind", "box")
    if kind == "group":
        node.update(x=round(x), y=round(y), w=round(width), h=round(height), title_font_size=36)
        return None
    entity = kind == "entity"
    inner_ratio = 0.66 if kind == "decision" else 1
    title_size, body_size, fitted_width = fitted_text_width(lines, width, entity, inner_ratio)
    inner_width = (fitted_width - 2 * PADDING) * inner_ratio
    rows = wrapped_rows(lines[1:], body_size, inner_width)
    text_height = title_size * 1.2 + (6 + rows * body_size * 1.26 if rows else 0)
    fitted_height = math.ceil(text_height / inner_ratio + 2 * PADDING)
    # 保持节点中心与分层顺序，使原来的关联方向仍易读；正文需要多行时按行给空间。
    node.update(x=round(x + (width - fitted_width) / 2), y=round(y + (height - fitted_height) / 2),
                w=math.ceil(fitted_width), h=fitted_height, title_font_size=title_size,
                body_font_size=body_size, title_single_line=True, text_align="left" if entity else "center")
    return {"node": node["id"], "title_size": title_size, "body_size": body_size,
            "width": node["w"], "height": node["h"]}


def fit_page(spec, page):
    entity_page = any(n.get("kind") == "entity" for n in page["nodes"])
    scale = 1.5 if entity_page else 1.3
    base = scale_original_layout(page, scale)
    changes = []
    for node in page["nodes"]:
        change = fit_node(node, base["nodes"][node["id"]], scale)
        if change is not None:
            changes.append({"file": spec["file"], "page": page["id"], **change})
    return changes


def main():
    changes = []
    for path in sorted((BASE / "specs").glob("*.json")):
        spec = json.loads(path.read_text(encoding="utf-8-sig"))
        for page in spec["pages"]:
            changes.extend(fit_page(spec, page))
        path.write_text(json.dumps(spec, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    report = {"node_title_range": [TITLE_MIN, TITLE_MAX], "body": BODY_SIZE,
              "entity_title": ENTITY_TITLE, "entity_fields": ENTITY_FIELDS, "nodes": changes}
    (BASE / "evidence/box-typography-layout.json").write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8")
    print(json.dumps({"nodes": len(changes), "node_title_range": [TITLE_MIN, TITLE_MAX], "body": BODY_SIZE,
                      "entity_title": ENTITY_TITLE, "entity_fields": ENTITY_FIELDS}, ensure_ascii=False))


if __name__ == "__main__":
    main()
