"""绘制带明确仿真水印的答题卡，按独立固定种子复现笔迹和扫描扰动。

这里仅渲染给定题例；来源文件与数据集清单由原命令入口负责。
"""

from __future__ import annotations

import hashlib
import io
import random
from pathlib import Path
from typing import Any

from PIL import Image, ImageDraw, ImageEnhance, ImageFilter, ImageFont

WIDTH = 1654


HEIGHT = 2339


FONT_SANS = Path("C:/Windows/Fonts/simhei.ttf")


FONT_SERIF = Path("C:/Windows/Fonts/simsun.ttc")


FONT_HAND = Path("C:/Windows/Fonts/STXINGKA.TTF")


FONT_KAI = Path("C:/Windows/Fonts/simkai.ttf")


def font(path: Path, size: int) -> ImageFont.FreeTypeFont:
    return ImageFont.truetype(str(path), size=size)


def text_size(draw: ImageDraw.ImageDraw, text: str, fnt: ImageFont.FreeTypeFont) -> tuple[int, int]:
    box = draw.textbbox((0, 0), text, font=fnt)
    return box[2] - box[0], box[3] - box[1]


def draw_centered(
    draw: ImageDraw.ImageDraw,
    xy: tuple[int, int],
    text: str,
    fnt: ImageFont.FreeTypeFont,
    fill: tuple[int, int, int],
) -> None:
    width, _ = text_size(draw, text, fnt)
    draw.text((xy[0] - width // 2, xy[1]), text, font=fnt, fill=fill)


# 用摘要生成可重复的条纹外观，不编码项目签名条码协议，只用于仿真版式。
def draw_barcode(draw: ImageDraw.ImageDraw, candidate_no: str) -> None:
    digest = hashlib.sha256(candidate_no.encode("utf-8")).digest()
    x = 1280
    y = 105
    for byte in digest[:24]:
        line_width = 2 + (byte % 4)
        height = 74 + (byte % 23)
        draw.rectangle((x, y, x + line_width, y + height), fill=(20, 20, 20))
        x += line_width + 3
    draw.text((1250, 205), candidate_no, font=font(FONT_SANS, 24), fill=(45, 45, 45))


def handwriting_patch(
    canvas: Image.Image,
    xy: tuple[int, int],
    text: str,
    size: int,
    seed: int,
    *,
    blur: float = 0.0,
) -> None:
    if not text:
        return
    # 固定种子只用于复现仿真笔迹的颜色与角度，不生成任何安全凭据。
    rng = random.Random(seed)
    fnt = font(FONT_HAND if seed % 2 == 0 else FONT_KAI, size)
    scratch = Image.new("RGBA", (1100, size * 2 + 30), (255, 255, 255, 0))
    sdraw = ImageDraw.Draw(scratch)
    color = (22 + rng.randint(0, 12), 36 + rng.randint(0, 16), 55 + rng.randint(0, 18), 245)
    sdraw.text((12, 3), text, font=fnt, fill=color)
    angle = rng.uniform(-2.0, 2.0)
    scratch = scratch.rotate(angle, resample=Image.Resampling.BICUBIC, expand=True)
    if blur:
        scratch = scratch.filter(ImageFilter.GaussianBlur(blur))
    canvas.alpha_composite(scratch, xy)


def draw_watermark(image: Image.Image) -> None:
    overlay = Image.new("RGBA", image.size, (255, 255, 255, 0))
    draw = ImageDraw.Draw(overlay)
    mark = "匿名仿真答题卡 · synthetic=true · 非真实学生"
    fnt = font(FONT_SANS, 34)
    for y in range(420, HEIGHT, 520):
        draw.text((270, y), mark, font=fnt, fill=(180, 35, 35, 35))
    image.alpha_composite(overlay)


def base_page(candidate_no: str, page_no: int) -> Image.Image:
    image = Image.new("RGBA", (WIDTH, HEIGHT), (250, 249, 246, 255))
    draw = ImageDraw.Draw(image)
    draw.rectangle((58, 58, WIDTH - 58, HEIGHT - 58), outline=(70, 70, 70), width=3)
    draw_centered(draw, (WIDTH // 2, 76), "2024年福建中考数学真实流程仿真答题卡", font(FONT_SANS, 40), (25, 25, 25))
    draw.text((100, 158), f"考生标识：{candidate_no}", font=font(FONT_SERIF, 29), fill=(35, 35, 35))
    draw.text((100, 206), f"页码：{page_no}/2", font=font(FONT_SERIF, 27), fill=(35, 35, 35))
    draw.text((100, 254), "说明：本卡为匿名仿真数据，不含真实学生身份，不用于商业训练。", font=font(FONT_SANS, 24), fill=(125, 28, 28))
    draw_barcode(draw, candidate_no)
    draw_watermark(image)
    return image


def _draw_choice_answers(
    draw: ImageDraw.ImageDraw,
    candidate: dict[str, Any],
    candidate_index: int,
    regions: dict[str, dict[str, float]],
) -> None:
    draw.text((100, 330), "一、选择题（每题4分）", font=font(FONT_SANS, 31), fill=(20, 20, 20))
    option_font = font(FONT_SANS, 27)
    for index in range(10):
        row = index // 5
        col = index % 5
        x0 = 105 + col * 302
        y0 = 402 + row * 160
        question_no = f"Q{index + 1}"
        draw.text((x0, y0), f"{index + 1}.", font=option_font, fill=(25, 25, 25))
        selected = candidate["answers"][question_no]["text"]
        selected_options = selected if isinstance(selected, list) else [selected]
        for option_index, option in enumerate("ABCD"):
            cx = x0 + 66 + option_index * 53
            cy = y0 + 26
            draw.ellipse((cx - 17, cy - 17, cx + 17, cy + 17), outline=(70, 70, 70), width=2)
            draw_centered(draw, (cx, cy - 14), option, font(FONT_SANS, 20), (70, 70, 70))
            if option in selected_options:
                # 每个选项独立取种子并画七笔；这里只改变仿真外观，不参与身份或评分判定。
                # 合并随机源会改变其他题目的仿真外观，须保留独立种子和调用顺序。
                rng = random.Random(candidate_index * 100 + index * 9 + option_index)
                for _ in range(7):
                    dx = rng.randint(-13, 13)
                    draw.line((cx - 12, cy + dx // 3, cx + 12, cy + dx), fill=(28, 37, 45), width=3)
        regions[question_no] = {
            "x": x0 / WIDTH,
            "y": (y0 - 20) / HEIGHT,
            "width": 270 / WIDTH,
            "height": 105 / HEIGHT,
        }


def _fill_answer_blur(confidence: float) -> float:
    # 这里只选择仿真字迹的模糊程度，不能用作评分或人工复核阈值。
    if confidence < 0.75:
        return 1.35
    if confidence < 0.9:
        return 0.35
    return 0.0


def page_one(candidate: dict[str, Any], candidate_index: int) -> tuple[Image.Image, dict[str, dict[str, float]]]:
    image = base_page(candidate["candidate_no"], 1)
    draw = ImageDraw.Draw(image)
    regions: dict[str, dict[str, float]] = {}
    _draw_choice_answers(draw, candidate, candidate_index, regions)
    draw.line((95, 720, WIDTH - 95, 720), fill=(120, 120, 120), width=2)
    draw.text((100, 760), "二、填空题（每题4分）", font=font(FONT_SANS, 31), fill=(20, 20, 20))
    fill_positions = [
        (110, 855), (860, 855),
        (110, 1080), (860, 1080),
        (110, 1305), (860, 1305),
    ]
    for offset, (x0, y0) in enumerate(fill_positions):
        question_number = offset + 11
        question_no = f"Q{question_number}"
        draw.text((x0, y0), f"{question_number}.", font=font(FONT_SANS, 29), fill=(25, 25, 25))
        draw.line((x0 + 72, y0 + 75, x0 + 620, y0 + 75), fill=(65, 65, 65), width=2)
        confidence = float(candidate["answers"][question_no]["confidence"])
        blur = _fill_answer_blur(confidence)
        handwriting_patch(
            image,
            (x0 + 96, y0 + 2),
            str(candidate["answers"][question_no]["text"]),
            55,
            seed=1000 + candidate_index * 100 + offset,
            blur=blur,
        )
        if confidence < 0.7 and candidate["answers"][question_no]["text"]:
            draw.line((x0 + 125, y0 + 38, x0 + 405, y0 + 56), fill=(85, 86, 90), width=5)
        regions[question_no] = {
            "x": x0 / WIDTH,
            "y": (y0 - 25) / HEIGHT,
            "width": 640 / WIDTH,
            "height": 145 / HEIGHT,
        }

    draw.rectangle((100, 1585, WIDTH - 100, 2200), outline=(150, 150, 150), width=2)
    draw.text((125, 1615), "扫描备注区（非作答区）", font=font(FONT_SANS, 24), fill=(95, 95, 95))
    draw.text((125, 1670), "本页包含选择题涂点与短文本填空，供 OMR/OCR 批量分流。", font=font(FONT_SERIF, 28), fill=(70, 70, 70))
    return image, regions


def wrap_by_chars(text: str, width: int) -> list[str]:
    lines: list[str] = []
    current = ""
    for char in text:
        current += char
        if len(current) >= width:
            lines.append(current)
            current = ""
    if current:
        lines.append(current)
    return lines


def page_two(candidate: dict[str, Any], candidate_index: int) -> tuple[Image.Image, dict[str, dict[str, float]]]:
    image = base_page(candidate["candidate_no"], 2)
    draw = ImageDraw.Draw(image)
    regions: dict[str, dict[str, float]] = {}
    draw.text((100, 330), "三、解答题（智能体建议 + 阅卷教师确认）", font=font(FONT_SANS, 31), fill=(20, 20, 20))

    boxes = {
        "Q17": (100, 410, WIDTH - 100, 850),
        "Q18": (100, 885, WIDTH - 100, 1515),
        "Q19": (100, 1550, WIDTH - 100, 2200),
    }
    for q_index, question_no in enumerate(["Q17", "Q18", "Q19"]):
        x1, y1, x2, y2 = boxes[question_no]
        draw.rounded_rectangle((x1, y1, x2, y2), radius=12, outline=(85, 85, 85), width=2)
        draw.text((x1 + 24, y1 + 18), f"{question_no[1:]}.", font=font(FONT_SANS, 30), fill=(25, 25, 25))
        answer_text = str(candidate["answers"][question_no]["text"])
        line_width = {"Q17": 26, "Q18": 24, "Q19": 27}[question_no]
        for line_index, line in enumerate(wrap_by_chars(answer_text, line_width)):
            handwriting_patch(
                image,
                (x1 + 78, y1 + 82 + line_index * 82),
                line,
                46 if question_no != "Q17" else 52,
                seed=2000 + candidate_index * 100 + q_index * 10 + line_index,
                blur=0.5 if candidate["answers"][question_no]["confidence"] < 0.8 else 0.0,
            )
        for line_y in range(y1 + 170, y2 - 24, 78):
            draw.line((x1 + 35, line_y, x2 - 35, line_y), fill=(220, 220, 220), width=1)
        regions[question_no] = {
            "x": x1 / WIDTH,
            "y": y1 / HEIGHT,
            "width": (x2 - x1) / WIDTH,
            "height": (y2 - y1) / HEIGHT,
        }
    return image, regions


def apply_scan_effects(image: Image.Image, scan: dict[str, float], seed: int) -> Image.Image:
    # 固定种子只复现噪点的位置和亮度，图像可重复性是仿真验收的前提。
    rng = random.Random(seed)
    rgb = image.convert("RGB")
    rgb = ImageEnhance.Contrast(rgb).enhance(float(scan["contrast"]))
    angle = float(scan["angle"])
    rgb = rgb.rotate(angle, resample=Image.Resampling.BICUBIC, expand=False, fillcolor=(244, 243, 239))
    pixels = rgb.load()
    noise = float(scan["noise"])
    for _ in range(int(WIDTH * HEIGHT * 0.0025)):
        x = rng.randrange(WIDTH)
        y = rng.randrange(HEIGHT)
        r, g, b = pixels[x, y]
        delta = int(rng.gauss(0, noise * 3))
        pixels[x, y] = (
            max(0, min(255, r + delta)),
            max(0, min(255, g + delta)),
            max(0, min(255, b + delta)),
        )
    if noise >= 3:
        rgb = rgb.filter(ImageFilter.GaussianBlur(0.28))
    encoded = io.BytesIO()
    rgb.save(encoded, format="JPEG", quality=91 if noise < 3 else 84, subsampling=1)
    encoded.seek(0)
    return Image.open(encoded).convert("RGB")


def write_pdf(images: list[Image.Image], path: Path) -> None:
    first, rest = images[0], images[1:]
    first.save(path, "PDF", resolution=200.0, save_all=True, append_images=rest)
