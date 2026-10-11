import importlib.util
import re
import sys
from pathlib import Path
from unittest.mock import Mock

import numpy as np
import pytest


@pytest.fixture
def tool(monkeypatch):
    path = Path(__file__).resolve().parents[1] / "tools" / "evaluate_external_omr.py"
    spec = importlib.util.spec_from_file_location("external_omr_batch40", path)
    module = importlib.util.module_from_spec(spec)
    monkeypatch.setitem(sys.modules, spec.name, module)
    spec.loader.exec_module(module)
    return module


@pytest.mark.parametrize("long_malformed_prefix", [False, True])
def test_parse_labels_keeps_unicode_nonanchored_and_last_match_semantics(
    tool, tmp_path, long_malformed_prefix
):
    text = "\n".join(f"{number}:A" for number in range(1, 91))
    text += "\n12 34:C\n9 : 12:B\n\u0661\u0662\u2003:\u00a0D\n1:Aextra\n\u00b2:B\n"
    expected = {int(number): label for number, label in re.findall(r"(\d+)\s*:\s*([ABCDXM])", text)}
    if long_malformed_prefix:
        # 长畸形串只交给新入口，避免测试本身运行旧正则的回溯路径。
        text = "9" * 20_000 + "!\n" + text
    path = tmp_path / "synthetic.txt"
    path.write_text(text, encoding="utf-8")

    assert tool.parse_labels(path) == expected
    assert expected[12] == "D"
    assert expected[34] == "C"
    assert expected[2] == "A"


@pytest.mark.parametrize("numbers", [range(1, 90), range(1, 92)])
def test_parse_labels_rejects_missing_or_extra_question_keys(tool, tmp_path, numbers):
    path = tmp_path / "synthetic.txt"
    path.write_text("\n".join(f"{number}:A" for number in numbers), encoding="utf-8")

    with pytest.raises(ValueError, match="does not contain one governed label for every item"):
        tool.parse_labels(path)


@pytest.mark.parametrize("sheet_count,missing_label", [(0, False), (1, False), (3, False), (2, True)])
def test_evaluate_rejects_invalid_dataset_before_registration(
    tool, tmp_path, monkeypatch, sheet_count, missing_label
):
    for index in range(sheet_count):
        image = tmp_path / f"{index}.jpg"
        image.write_bytes(b"synthetic-image")
        if not missing_label or index != 0:
            image.with_suffix(".txt").write_text("1:A", encoding="utf-8")
    register = Mock(side_effect=AssertionError("registration must not run"))
    monkeypatch.setattr(tool, "register_answer_area", register)
    message = "human-observed label file" if missing_label else "even number of at least two"

    with pytest.raises(ValueError, match=message):
        tool.evaluate(tmp_path)

    register.assert_not_called()


def test_evaluate_keeps_opposite_fold_reference_timing_and_gate_counts(tool, tmp_path, monkeypatch):
    # 只隔离昂贵的图像处理；合成数组仍经过真实两折参考、标签解析和指标汇总。
    labels = [("A", "X"), ("B", "M"), ("C", "A"), ("D", "X")]
    for value, pair in zip((10, 20, 30, 40), labels, strict=True):
        image = tmp_path / f"{value}.jpg"
        image.write_bytes(bytes([value]))
        image.with_suffix(".txt").write_text(f"1:{pair[0]}\n2:{pair[1]}", encoding="utf-8")
    monkeypatch.setattr(tool, "QUESTION_COUNT", 2)
    monkeypatch.setattr(tool, "register_answer_area", lambda path: np.full((1, 1), int(path.stem), dtype=np.uint8))
    events = []

    def crop(image, question_no):
        events.append("crop")
        return np.asarray([[int(image[0, 0]), question_no]], dtype=np.uint8), [{"label": "A"}]

    def encode(image):
        events.append("encode")
        return image.tobytes()

    ticks = iter(range(16))

    def clock():
        events.append("clock")
        return next(ticks) * 1_000_000

    cases = [
        ("selected", ["A"], 0.98, False),
        ("blank", [], 0.99, False),
        ("selected", ["A"], 0.99, False),
        ("multiple", ["A", "B"], 0.99, True),
        ("selected", ["C"], 0.97999, False),
        ("ambiguous", [], 0.99, True),
        ("selected", ["D"], 0.98, True),
        ("selected", ["A"], 0.99, False),
    ]

    def extract(*_args, **_kwargs):
        events.append("extract")
        decision, selected, confidence, review = cases[extractor.call_count - 1]
        return {"decision": decision, "selected": selected, "confidence": confidence, "needs_human_review": review}

    extractor = Mock(side_effect=extract)
    monkeypatch.setattr(tool, "question_crop", crop)
    monkeypatch.setattr(tool, "encode_png", encode)
    monkeypatch.setattr(tool.time, "perf_counter_ns", clock)
    monkeypatch.setattr(tool, "extract_marks", extractor)

    report = tool.evaluate(tmp_path)

    observed = [
        (call.args[0], call.kwargs["reference_image_bytes"])
        for call in extractor.call_args_list
    ]
    assert observed == [
        (bytes([value, question]), bytes([35 if value < 30 else 15, question]))
        for value in (10, 20, 30, 40) for question in (1, 2)
    ]
    assert events == ["crop", "crop", "clock", "encode", "encode", "extract", "clock"] * 8
    first = extractor.call_args_list[0]
    assert first.kwargs["profile"].mode == "template_difference"
    assert first.kwargs["profile"].marked_threshold == 0.18
    assert first.kwargs["profile"].minimum_margin == 0.06
    assert first.kwargs["reference_content_type"] == "image/png"
    assert first.kwargs["reference_question_region"] == {"x": 0, "y": 0, "width": 1, "height": 1}
    assert report["decision_counts"] == {"selected": 5, "blank": 1, "multiple": 1, "ambiguous": 1}
    assert report["raw_extractor"] == {
        "clear_label_count": 5,
        "correct_selected_count": 2,
        "wrong_selected_count": 1,
        "clear_routed_to_review_count": 2,
        "unsafe_label_count": 3,
        "unsafe_routed_to_review_count": 1,
        "unsafe_selected_count": 2,
        "selected_precision": 0.4,
        "clear_selected_coverage": 0.6,
        "unsafe_review_recall": pytest.approx(1 / 3),
    }
    assert report["production_gate"] == {
        "automatic_count": 4,
        "correct_automatic_count": 1,
        "wrong_automatic_count": 3,
        "manual_review_count": 4,
        "unsafe_automatic_count": 2,
        "unsafe_routed_to_review_count": 1,
        "silent_unsafe_zero_count": 1,
        "automatic_precision": 0.25,
        "automatic_coverage": 0.5,
        "clear_automatic_coverage": 0.2,
        "manual_routing_rate": 0.5,
        "unsafe_review_recall": pytest.approx(1 / 3),
    }
    assert report["throughput"]["average_duration_ms"] == 1.0
    assert report["throughput"]["p95_duration_ms"] == 1.0
    assert report["throughput"]["sequential_items_per_second"] == 1000.0
    assert report["dataset"]["local_item_count"] == 8
    assert report["dataset"]["manifest_sha256"] == tool.dataset_manifest_hash(tmp_path, list(tmp_path.iterdir()))
    assert report["method"]["auto_confirm_minimum_confidence"] == 0.98
    assert report["verdict"]["auto_confirmation_profile_approved"] is False
    assert report["verdict"]["required_mode"] == "manual_only"
