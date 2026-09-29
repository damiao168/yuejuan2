from __future__ import annotations

import hashlib
import io
import json
import random
from pathlib import Path
from typing import Any

from PIL import Image, ImageDraw, ImageEnhance, ImageFilter, ImageFont
from pypdf import PdfReader, PdfWriter


ROOT = Path(__file__).resolve().parents[1]
SOURCE_PDF = ROOT / "tmp" / "pdfs" / "fujian-2024-junior-high-exam-all-subjects.pdf"
DATASET_DIR = ROOT / "lab" / "evals" / "synthetic" / "fujian-2024-junior-math-v1"
ANSWER_DIR = DATASET_DIR / "answer-sheets"
REFERENCE_DIR = DATASET_DIR / "references"

WIDTH = 1654
HEIGHT = 2339

FONT_SANS = Path("C:/Windows/Fonts/simhei.ttf")
FONT_SERIF = Path("C:/Windows/Fonts/simsun.ttc")
FONT_HAND = Path("C:/Windows/Fonts/STXINGKA.TTF")
FONT_KAI = Path("C:/Windows/Fonts/simkai.ttf")

SOURCE_PAGE = (
    "https://jyj.quanzhou.gov.cn/wsbs/bgxz/202406/"
    "t20240621_3049176.htm"
)
SOURCE_ATTACHMENT = (
    "https://jyj.quanzhou.gov.cn/wsbs/bgxz/202406/"
    "P020240621550310585717.pdf"
)

CHOICE_KEY = ["D", "C", "C", "A", "B", "B", "A", "A", "B", "C"]
FILL_KEY = ["x(x+1)", "x<1", "90", "2", "(2,1)", "128"]

QUESTION_STEMS = {
    "Q1": "下列实数中，无理数是（ ）",
    "Q2": "用科学记数法表示 69610。",
    "Q3": "判断所给几何体的左视图。",
    "Q4": "根据平行线与角的关系求角度。",
    "Q5": "计算幂的乘除。",
    "Q6": "从三个相同小球中随机抽取，求指定事件概率。",
    "Q7": "根据圆与三角形关系求角度。",
    "Q8": "根据一次函数图象判断方程组的解。",
    "Q9": "判断蝴蝶图案中轴对称推断不正确的一项。",
    "Q10": "判断关于二次方程的两个说法。",
    "Q11": "分解因式：x²+x=____。",
    "Q12": "不等式的解集为____。",
    "Q13": "求一组数据的中位数。",
    "Q14": "求坐标系中阴影图形面积。",
    "Q15": "反比例函数与圆交于 A、B，已知 A(1,2)，求 B 坐标。",
    "Q16": "根据帆船受力模型求 f₂ 的大小（N）。",
    "Q17": "计算：(-1)⁰+|-5|-√4。",
    "Q18": "菱形 ABCD 中，E、F 分别在 BC、CD 上，且∠AEB=∠AFD。证明 BE=DF。",
    "Q19": "解方程：3/(x+2)+1=x/(x-2)。",
}

SUBJECTIVE_REFERENCES = {
    "Q17": "原式=1+5-2=4。",
    "Q18": (
        "因为四边形ABCD是菱形，所以AB=AD，∠B=∠D。"
        "又∠AEB=∠AFD，因此△ABE≌△ADF，所以BE=DF。"
    ),
    "Q19": (
        "方程两边同乘(x+2)(x-2)，得"
        "3(x-2)+(x+2)(x-2)=x(x+2)，解得x=10；检验成立。"
    ),
}


def answer(
    text: str | list[str],
    confidence: float,
    route: str,
    *,
    score: float | None = None,
) -> dict[str, Any]:
    return {
        "text": text,
        "confidence": confidence,
        "route": route,
        "expected_score": score,
        "needs_human_review": route != "auto_confirm",
        "synthetic": True,
    }


# 答案、置信度和分流结果均为手工设定的仿真预期，不能当作 OCR 或评分模型实测表现。
CANDIDATES: list[dict[str, Any]] = [
    {
        "candidate_no": "SIM-FJ2024-001",
        "profile": "清晰、完整、答案正确；客观题与填空题应自动通过。",
        "scan": {"angle": -0.25, "noise": 1.2, "contrast": 0.99},
        "answers": {
            **{
                f"Q{i + 1}": answer(v, 0.985, "auto_confirm", score=4)
                for i, v in enumerate(CHOICE_KEY)
            },
            **{
                f"Q{i + 11}": answer(v, 0.965, "auto_confirm", score=4)
                for i, v in enumerate(FILL_KEY)
            },
            "Q17": answer("1+5-2=4", 0.94, "subjective_agent_review", score=8),
            "Q18": answer(
                "AB=AD，∠B=∠D；又∠AEB=∠AFD，故△ABE≌△ADF，所以BE=DF。",
                0.89,
                "subjective_agent_review",
                score=8,
            ),
            "Q19": answer(
                "两边同乘(x+2)(x-2)：3(x-2)+(x+2)(x-2)=x(x+2)，x=10，检验成立。",
                0.91,
                "subjective_agent_review",
                score=8,
            ),
        },
    },
    {
        "candidate_no": "SIM-FJ2024-002",
        "profile": "字迹清晰但存在确定性错误，用于验证自动评分不是“自动判对”。",
        "scan": {"angle": 0.35, "noise": 1.8, "contrast": 0.97},
        "answers": {
            **{
                f"Q{i + 1}": answer(v, 0.975, "auto_confirm", score=4 if v == CHOICE_KEY[i] else 0)
                for i, v in enumerate(["D", "C", "C", "A", "A", "B", "A", "A", "D", "C"])
            },
            "Q11": answer("x(x+1)", 0.96, "auto_confirm", score=4),
            "Q12": answer("x≤1", 0.95, "auto_confirm", score=0),
            "Q13": answer("90", 0.97, "auto_confirm", score=4),
            "Q14": answer("2", 0.96, "auto_confirm", score=4),
            "Q15": answer("(1,2)", 0.94, "auto_confirm", score=0),
            "Q16": answer("128", 0.96, "auto_confirm", score=4),
            "Q17": answer("1+5-2=4", 0.94, "subjective_agent_review", score=8),
            "Q18": answer(
                "∠AEB=∠AFD，所以△ABE≌△ADF，所以BE=DF。",
                0.91,
                "subjective_agent_review",
                score=4,
            ),
            "Q19": answer(
                "3(x-2)+(x+2)(x-2)=x(x+2)，算得x=8。",
                0.9,
                "subjective_agent_review",
                score=2,
            ),
        },
    },
    {
        "candidate_no": "SIM-FJ2024-003",
        "profile": "含涂改、多选、空答和模糊字迹，必须进入人工复核。",
        "scan": {"angle": -0.7, "noise": 3.0, "contrast": 0.94},
        "answers": {
            **{
                f"Q{i + 1}": answer(
                    v,
                    0.51 if i == 3 else 0.97,
                    "omr_review" if i == 3 else "auto_confirm",
                    score=None if i == 3 else 4,
                )
                for i, v in enumerate(["D", "C", "C", ["A", "C"], "B", "B", "A", "A", "B", "C"])
            },
            "Q11": answer("x(x+1)", 0.72, "rule_grade_review", score=4),
            "Q12": answer("x<1", 0.95, "auto_confirm", score=4),
            "Q13": answer("90", 0.96, "auto_confirm", score=4),
            "Q14": answer("", 0.2, "rule_grade_review", score=0),
            "Q15": answer("(2,1)", 0.93, "auto_confirm", score=4),
            "Q16": answer("128", 0.68, "rule_grade_review", score=4),
            "Q17": answer("1+5-√4=4", 0.82, "subjective_agent_review", score=8),
            "Q18": answer(
                "AB=AD，∠B=∠D，且∠AEB=∠AFD，△ABE≌△ADF，BE=DF。",
                0.76,
                "subjective_agent_review",
                score=8,
            ),
            "Q19": answer(
                "去分母后整理得x=10。",
                0.73,
                "subjective_agent_review",
                score=5,
            ),
        },
    },
    {
        "candidate_no": "SIM-FJ2024-004",
        "profile": "扫描倾斜且局部压缩，混合高低置信度，用于批量OCR分流。",
        "scan": {"angle": 0.95, "noise": 4.2, "contrast": 0.91},
        "answers": {
            **{
                f"Q{i + 1}": answer(v, 0.94, "auto_confirm", score=4 if v == CHOICE_KEY[i] else 0)
                for i, v in enumerate(["D", "C", "B", "A", "B", "B", "A", "A", "B", "C"])
            },
            "Q11": answer("x²+x?", 0.84, "rule_grade_review", score=0),
            "Q12": answer("x<1", 0.91, "auto_confirm", score=4),
            "Q13": answer("90", 0.94, "auto_confirm", score=4),
            "Q14": answer("2", 0.95, "auto_confirm", score=4),
            "Q15": answer("(2,1)", 0.91, "auto_confirm", score=4),
            "Q16": answer("128", 0.93, "auto_confirm", score=4),
            "Q17": answer("(-1)⁰+5-2=4", 0.88, "subjective_agent_review", score=8),
            "Q18": answer(
                "菱形有AB=AD。两角相等，所以两个三角形全等，BE=DF。",
                0.81,
                "subjective_agent_review",
                score=6,
            ),
            "Q19": answer(
                "3(x-2)+(x+2)(x-2)=x(x+2)，x=10。",
                0.87,
                "subjective_agent_review",
                score=7,
            ),
        },
    },
]


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


def page_one(candidate: dict[str, Any], candidate_index: int) -> tuple[Image.Image, dict[str, dict[str, float]]]:
    image = base_page(candidate["candidate_no"], 1)
    draw = ImageDraw.Draw(image)
    regions: dict[str, dict[str, float]] = {}

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
        blur = 1.35 if confidence < 0.75 else 0.35 if confidence < 0.9 else 0.0
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


def extract_pdf_pages(reader: PdfReader, start: int, stop: int, target: Path) -> None:
    writer = PdfWriter()
    for index in range(start, stop):
        writer.add_page(reader.pages[index])
    with target.open("wb") as handle:
        writer.write(handle)


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def build_questions() -> list[dict[str, Any]]:
    result: list[dict[str, Any]] = []
    for index in range(1, 20):
        question_no = f"Q{index}"
        if index <= 10:
            qtype = "single_choice"
            key: Any = CHOICE_KEY[index - 1]
        elif index <= 16:
            qtype = "fill_blank"
            key = FILL_KEY[index - 11]
        else:
            qtype = "calculation"
            key = SUBJECTIVE_REFERENCES[question_no]
        result.append(
            {
                "question_no": question_no,
                "question_type": qtype,
                "score": 4 if index <= 16 else 8,
                "stem": QUESTION_STEMS[question_no],
                "standard_answer": key,
                "synthetic": False,
                "source": "official_fujian_2024_junior_high_math",
            }
        )
    return result


def main() -> None:
    if not SOURCE_PDF.exists():
        raise SystemExit(f"missing official source PDF: {SOURCE_PDF}")
    ANSWER_DIR.mkdir(parents=True, exist_ok=True)
    REFERENCE_DIR.mkdir(parents=True, exist_ok=True)

    reader = PdfReader(str(SOURCE_PDF))
    if len(reader.pages) < 24:
        raise SystemExit(f"official source PDF has only {len(reader.pages)} pages")
    question_pdf = REFERENCE_DIR / "official-fujian-2024-math-questions.pdf"
    answer_pdf = REFERENCE_DIR / "official-fujian-2024-math-reference-answers.pdf"
    # 页码为此固定来源 PDF 的零基、左闭右开区间；更换源文件时必须重新核对科目页。
    extract_pdf_pages(reader, 10, 17, question_pdf)
    extract_pdf_pages(reader, 17, 24, answer_pdf)

    blank_candidate = {
        "candidate_no": "TEMPLATE",
        "answers": {
            f"Q{index}": answer("", 1.0, "template")
            for index in range(1, 20)
        },
    }
    blank_page1, _ = page_one(blank_candidate, 99)
    blank_page2, _ = page_two(blank_candidate, 99)
    blank_images = [blank_page1.convert("RGB"), blank_page2.convert("RGB")]
    blank_page1_path = REFERENCE_DIR / "blank-answer-sheet-page-1.png"
    blank_page2_path = REFERENCE_DIR / "blank-answer-sheet-page-2.png"
    blank_pdf_path = REFERENCE_DIR / "blank-answer-sheet-template.pdf"
    blank_images[0].save(blank_page1_path, format="PNG", optimize=True)
    blank_images[1].save(blank_page2_path, format="PNG", optimize=True)
    write_pdf(blank_images, blank_pdf_path)

    candidate_records: list[dict[str, Any]] = []
    all_regions: dict[str, dict[str, dict[str, float]]] = {}
    for candidate_index, candidate in enumerate(CANDIDATES):
        page1, page1_regions = page_one(candidate, candidate_index)
        page2, page2_regions = page_two(candidate, candidate_index)
        rendered = [
            apply_scan_effects(page1, candidate["scan"], candidate_index * 2 + 1),
            apply_scan_effects(page2, candidate["scan"], candidate_index * 2 + 2),
        ]
        page_paths: list[str] = []
        for page_index, page_image in enumerate(rendered, start=1):
            path = ANSWER_DIR / f"{candidate['candidate_no']}-page-{page_index}.png"
            page_image.save(path, format="PNG", optimize=True)
            page_paths.append(path.relative_to(DATASET_DIR).as_posix())
        pdf_path = ANSWER_DIR / f"{candidate['candidate_no']}.pdf"
        write_pdf(rendered, pdf_path)
        candidate_records.append(
            {
                **candidate,
                "synthetic": True,
                "contains_real_student_identity": False,
                "training_allowed": False,
                "page_files": page_paths,
                "pdf_file": pdf_path.relative_to(DATASET_DIR).as_posix(),
            }
        )
        # 区域坐标以未旋转模板的宽高归一化；这里不会跟随扫描旋转变换，使用时须结合配准。
        all_regions[candidate["candidate_no"]] = {**page1_regions, **page2_regions}

    questions = build_questions()
    sources = {
        "synthetic": True,
        "training_allowed": False,
        "official_exam": {
            "title": "2024年福建省初中学业水平考试试题、参考答案（数学）",
            "publisher_page": SOURCE_PAGE,
            "attachment": SOURCE_ATTACHMENT,
            "publisher": "泉州市教育局（页面注明来源：福建省教育考试院）",
            "retrieved_on": "2026-07-16",
            "local_source_sha256": sha256(SOURCE_PDF),
            "use": "非商业产品流程仿真与人工验证；保留来源，不作为训练语料。",
        },
        "real_handwriting_reference": {
            "url": "https://blog.csdn.net/QQ_1309399183/article/details/149368962",
            "license_notice": "页面标注 CC BY-SA 4.0",
            "imported": False,
            "reason": "样本含身份字段且并非同一试卷，仅用于观察真实扫描、手写和版式难度。",
        },
        "answer_sheet_rules": {
            "url": "https://www.bjeea.cn/html/ksb/zhongyaoxinwen/2024/0716/85579.html",
            "use": "参考答题区域、2B铅笔选择题与黑色签字笔作答等真实流程约束。",
        },
    }
    manifest = {
        "dataset_id": "fujian-2024-junior-math-v1",
        "title": "2024福建中考数学真实流程匿名仿真数据集",
        "version": "1.0.0",
        "created_at": "2026-07-16T00:00:00+08:00",
        "synthetic": True,
        "simulation_only": True,
        "training_allowed": False,
        "contains_real_student_identity": False,
        "candidate_count": len(candidate_records),
        "page_count": len(candidate_records) * 2,
        "question_count": len(questions),
        "scope": {
            "choice_questions": 10,
            "fill_blank_questions": 6,
            "subjective_questions": 3,
            "total_score": 88,
        },
        "automation_policy": {
            "objective_auto_confirm_threshold": 0.9,
            "ocr_review_below": 0.9,
            "subjective_always_human_review": True,
            "low_confidence_never_publishes_score": True,
        },
        "files": {
            "questions": "questions.json",
            "candidates": "candidates.json",
            "regions": "regions.json",
            "sources": "sources.json",
            "official_questions": question_pdf.relative_to(DATASET_DIR).as_posix(),
            "official_answers": answer_pdf.relative_to(DATASET_DIR).as_posix(),
            "blank_answer_sheet": blank_pdf_path.relative_to(DATASET_DIR).as_posix(),
            "blank_answer_sheet_pages": [
                blank_page1_path.relative_to(DATASET_DIR).as_posix(),
                blank_page2_path.relative_to(DATASET_DIR).as_posix(),
            ],
        },
    }

    (DATASET_DIR / "questions.json").write_text(
        json.dumps(questions, ensure_ascii=False, indent=2) + "\n",
        encoding="utf-8",
    )
    (DATASET_DIR / "candidates.json").write_text(
        json.dumps(candidate_records, ensure_ascii=False, indent=2) + "\n",
        encoding="utf-8",
    )
    (DATASET_DIR / "regions.json").write_text(
        json.dumps(all_regions, ensure_ascii=False, indent=2) + "\n",
        encoding="utf-8",
    )
    (DATASET_DIR / "sources.json").write_text(
        json.dumps(sources, ensure_ascii=False, indent=2) + "\n",
        encoding="utf-8",
    )
    (DATASET_DIR / "manifest.json").write_text(
        json.dumps(manifest, ensure_ascii=False, indent=2) + "\n",
        encoding="utf-8",
    )
    print(
        json.dumps(
            {
                "dataset_dir": str(DATASET_DIR),
                "candidates": len(candidate_records),
                "pages": len(candidate_records) * 2,
                "questions": len(questions),
                "synthetic": True,
            },
            ensure_ascii=False,
        )
    )


if __name__ == "__main__":
    main()
