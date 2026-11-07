from __future__ import annotations

import hashlib
import io
import json
import random
from pathlib import Path
from typing import Any

from PIL import Image, ImageDraw, ImageEnhance, ImageFilter, ImageFont
from pypdf import PdfReader, PdfWriter


# CLI 和按文件导入都没有稳定的包名；只在装载同目录模块时补搜索路径，随后恢复。
import sys

_support_dir = str(Path(__file__).resolve().parent)
_added_support_path = _support_dir not in sys.path
if _added_support_path:
    sys.path.insert(0, _support_dir)
try:
    from fujian_math_simulation_data import (
        CHOICE_KEY,
        FACTORED_ANSWER,
        COORDINATE_ANSWER,
        FILL_KEY,
        QUESTION_STEMS,
        SUBJECTIVE_REFERENCES,
        answer,
        CANDIDATES,
        build_questions,
    )
    from fujian_math_simulation_render import (
        WIDTH,
        HEIGHT,
        FONT_SANS,
        FONT_SERIF,
        FONT_HAND,
        FONT_KAI,
        font,
        text_size,
        draw_centered,
        draw_barcode,
        handwriting_patch,
        draw_watermark,
        base_page,
        _draw_choice_answers,
        _fill_answer_blur,
        page_one,
        wrap_by_chars,
        page_two,
        apply_scan_effects,
        write_pdf,
    )
finally:
    if _added_support_path:
        sys.path.remove(_support_dir)
del _support_dir, _added_support_path


ROOT = Path(__file__).resolve().parents[1]
SOURCE_PDF = ROOT / "tmp" / "pdfs" / "fujian-2024-junior-high-exam-all-subjects.pdf"
DATASET_DIR = ROOT / "lab" / "evals" / "synthetic" / "fujian-2024-junior-math-v1"
ANSWER_DIR = DATASET_DIR / "answer-sheets"
REFERENCE_DIR = DATASET_DIR / "references"


SOURCE_PAGE = (
    "https://jyj.quanzhou.gov.cn/wsbs/bgxz/202406/"
    "t20240621_3049176.htm"
)
SOURCE_ATTACHMENT = (
    "https://jyj.quanzhou.gov.cn/wsbs/bgxz/202406/"
    "P020240621550310585717.pdf"
)






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
