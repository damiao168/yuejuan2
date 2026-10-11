import ast
import copy
import random
from pathlib import Path
from types import SimpleNamespace

import pytest

SOURCE = Path(__file__).with_name("generate-fujian-2024-math-simulation.py")
WIDTH = 1654
HEIGHT = 2339
TRACE_CHOICE_ID = "Q4"
FILL_SEED = 1200


def _functions() -> dict:
    tree = ast.parse(SOURCE.read_bytes(), filename=str(SOURCE))
    names = {"_draw_choice_answers", "_fill_answer_blur", "page_one"}
    # 只执行本次改动的函数定义；不加载 Pillow/pypdf，也不执行候选构造和生成入口。
    selected = [node for node in tree.body if isinstance(node, ast.FunctionDef) and node.name in names]
    assert {node.name for node in selected} == names
    future = ast.ImportFrom(module="__future__", names=[ast.alias(name="annotations")], level=0)
    module = ast.fix_missing_locations(ast.Module(body=[future, *selected], type_ignores=[]))
    namespace = {"WIDTH": WIDTH, "HEIGHT": HEIGHT, "FONT_SANS": "sans", "FONT_SERIF": "serif"}
    exec(compile(module, str(SOURCE), "exec"), namespace)
    return namespace


def _candidate() -> dict:
    return {
        "synthetic": True,
        "candidate_no": "SYNTHETIC-TRACE",
        "answers": {f"Q{index}": {"text": "", "confidence": 1.0} for index in range(1, 17)},
    }


def _trace_page(candidate: dict) -> tuple[list, object, dict]:
    namespace = _functions()
    events = []
    image = object()

    class Draw:
        def __getattr__(self, name: str):
            assert name in {"text", "ellipse", "line", "rectangle"}
            return lambda *args, **kwargs: events.append((name, args, kwargs))

    class RecordedRandom:
        def __init__(self, seed: int):
            events.append(("seed", (seed,), {}))
            self.rng = random.Random(seed)

        def randint(self, low: int, high: int) -> int:
            events.append(("randint", (low, high), {}))
            return self.rng.randint(low, high)

    draw = Draw()

    def base_page(candidate_no: str, page_no: int) -> object:
        events.append(("base", (candidate_no, page_no), {}))
        return image

    def draw_image(actual_image: object) -> Draw:
        assert actual_image is image
        events.append(("draw", (), {}))
        return draw

    def font(path: str, size: int) -> tuple:
        events.append(("font", (path, size), {}))
        return path, size

    def centered(actual_draw: Draw, *args, **kwargs) -> None:
        assert actual_draw is draw
        events.append(("centered", args, kwargs))

    def handwriting(actual_image: object, *args, **kwargs) -> None:
        assert actual_image is image
        events.append(("handwriting", args, kwargs))

    namespace.update(
        base_page=base_page, ImageDraw=SimpleNamespace(Draw=draw_image), font=font,
        draw_centered=centered, handwriting_patch=handwriting,
        random=SimpleNamespace(Random=RecordedRandom),
    )
    returned_image, regions = namespace["page_one"](candidate, 2)
    assert returned_image is image
    return events, image, regions


@pytest.mark.parametrize(
    ("confidence", "expected"),
    [(float("-inf"), 1.35), (0.74, 1.35), (0.75, 0.35), (0.899, 0.35),
     (0.9, 0.0), (1.0, 0.0), (float("inf"), 0.0), (float("nan"), 0.0)],
)
def test_fill_blur_preserves_comparison_boundaries(confidence: float, expected: float) -> None:
    assert _functions()["_fill_answer_blur"](confidence) == expected


@pytest.mark.parametrize(
    ("selected", "seeds"),
    [("", []), ("A", [227]), (["A", "C"], [227, 229]), (["A", "A"], [227])],
)
def test_choice_trace_keeps_option_order_and_independent_seed(selected, seeds: list) -> None:
    candidate = _candidate()
    candidate["answers"][TRACE_CHOICE_ID]["text"] = selected
    original = copy.deepcopy(candidate)
    events, _, regions = _trace_page(candidate)
    assert candidate == original
    assert [event[1][0] for event in events if event[0] == "seed"] == seeds
    assert sum(event[0] == "ellipse" for event in events) == 40
    assert [event[1][1] for event in events if event[0] == "centered"] == list("ABCD") * 10
    assert [event[1] for event in events if event[0] == "randint"] == [(-13, 13)] * (7 * len(seeds))
    strokes = [event for event in events if event[0] == "line" and event[2].get("width") == 3]
    assert len(strokes) == 7 * len(seeds)
    for seed_index in (index for index, event in enumerate(events) if event[0] == "seed"):
        assert events[seed_index - 1][0] == "centered"
        assert [event[0] for event in events[seed_index + 1:seed_index + 15]] == ["randint", "line"] * 7
    assert list(regions) == [f"Q{index}" for index in range(1, 17)]
    assert regions["Q1"] == {"x": 105 / WIDTH, "y": 382 / HEIGHT, "width": 270 / WIDTH, "height": 105 / HEIGHT}
    assert regions["Q10"] == {"x": 1313 / WIDTH, "y": 542 / HEIGHT, "width": 270 / WIDTH, "height": 105 / HEIGHT}


@pytest.mark.parametrize(
    ("confidence", "text", "blur", "crossed"),
    [(0.699, "answer", 1.35, True), (0.699, "", 1.35, False),
     (0.7, "answer", 1.35, False), (0.75, "", 0.35, False), (0.9, "answer", 0.0, False)],
)
def test_fill_trace_keeps_text_seed_and_separate_crossing_boundary(
    confidence: float, text: str, blur: float, crossed: bool,
) -> None:
    candidate = _candidate()
    candidate["answers"]["Q11"] = {"text": text, "confidence": confidence}
    events, _, regions = _trace_page(candidate)
    patches = [event for event in events if event[0] == "handwriting"]
    assert len(patches) == 6
    assert patches[0] == ("handwriting", ((206, 857), text, 55), {"seed": FILL_SEED, "blur": blur})
    assert [event[2]["seed"] for event in patches] == [1200, 1201, 1202, 1203, 1204, 1205]
    assert [event[1][0] for event in patches] == [(206, 857), (956, 857), (206, 1082), (956, 1082), (206, 1307), (956, 1307)]
    crossed_lines = [event for event in events if event[0] == "line" and event[2].get("width") == 5]
    assert crossed_lines == ([("line", ((235, 893, 515, 911),), {"fill": (85, 86, 90), "width": 5})] if crossed else [])
    if crossed:
        patch_index = events.index(patches[0])
        assert events[patch_index + 1] == crossed_lines[0]
    assert regions["Q11"] == {"x": 110 / WIDTH, "y": 830 / HEIGHT, "width": 640 / WIDTH, "height": 145 / HEIGHT}
    assert regions["Q16"] == {"x": 860 / WIDTH, "y": 1280 / HEIGHT, "width": 640 / WIDTH, "height": 145 / HEIGHT}
    assert events[0] == ("base", ("SYNTHETIC-TRACE", 1), {})
    assert events[1] == ("draw", (), {})
    assert events[-1][0] == "text"
    assert events[-1][1][0] == (125, 1670)


@pytest.mark.parametrize("invalid", ["missing_choice_text", "invalid_confidence"])
def test_page_one_preserves_input_errors(invalid: str) -> None:
    candidate = _candidate()
    if invalid == "missing_choice_text":
        del candidate["answers"][TRACE_CHOICE_ID]["text"]
        with pytest.raises(KeyError) as error:
            _trace_page(candidate)
        assert error.value.args == ("text",)
    else:
        candidate["answers"]["Q11"]["confidence"] = "invalid"
        with pytest.raises(ValueError, match="could not convert string to float"):
            _trace_page(candidate)
