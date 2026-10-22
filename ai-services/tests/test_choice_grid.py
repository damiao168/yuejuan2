import base64
import copy
import hashlib
import json
import threading
from unittest.mock import patch
from urllib import error as urlerror
from urllib import request as urlrequest

import cv2
import numpy as np
import pytest
from helpers import FakeModel, settings

from grading_agent.app import GradingAgentApplication
from grading_agent.choice_grid import ObjectiveGridAnalyzer
from grading_agent.choice_grid_brackets import _split_attached_bracket
from grading_agent.choice_grid_schema import SCHEMA_VERSION
from grading_agent.errors import AgentError
from grading_agent.server import GradingAgentHTTPServer


def draw_option(image, center, shape="rectangle", filled=False):
    color, thickness = (0, 0, 0), -1 if filled else 2
    x, y = center
    if shape == "rectangle":
        cv2.rectangle(image, (x - 13, y - 9), (x + 13, y + 9), color, thickness)
    elif shape == "circle":
        cv2.circle(image, (x, y), 11, color, thickness)
    else:
        cv2.ellipse(image, (x, y), (15, 9), 0, 0, 360, color, thickness)


def grid_image(shape="rectangle", filled=False, columns=2, rows=3):
    image = np.full((1000, 760, 3), 255, np.uint8)
    for column in range(columns):
        for row in range(rows):
            for option in range(4):
                draw_option(image, (150 + column * 320 + option * 45, 220 + row * 90), shape,
                            filled=filled and option == row % 4)
    return image


def payload(image=None, bbox=None, orientation="horizontal", option_count=4):
    image = grid_image() if image is None else image
    raw = cv2.imencode(".png", image)[1].tobytes()
    return {"request_id": "objective-grid-test-0001", "schema_version": SCHEMA_VERSION,
            "page": {"page_no": 1, "width": image.shape[1], "height": image.shape[0], "media_type": "image/png",
                     "data_base64": base64.b64encode(raw).decode(), "sha256": hashlib.sha256(raw).hexdigest()},
            "block_bbox": bbox or {"x": .08, "y": .16, "width": .84, "height": .6},
            "option_count": option_count, "option_orientation": orientation}


@pytest.mark.parametrize("shape", ["rectangle", "circle", "ellipse"])
@pytest.mark.parametrize("filled", [False, True])
def test_real_cv_finds_hollow_and_marked_option_groups_across_columns(shape, filled):
    request = payload(grid_image(shape, filled))
    original = copy.deepcopy(request)
    result = ObjectiveGridAnalyzer().analyze(request)
    assert request == original
    assert result["status"] == "review_required"
    assert result["coordinate_space"] == "source_display_normalized"
    assert len(result["groups"]) == 6
    assert all(len(group["options"]) == 4 for group in result["groups"])
    assert len({group["group_id"] for group in result["groups"]}) == 6
    assert result == ObjectiveGridAnalyzer().analyze(request)
    for group in result["groups"]:
        assert group["reason_codes"] == ["GEOMETRY_ONLY_REQUIRES_REVIEW"]
        assert "question_id" not in group
        assert all(set(option) == {"bbox"} for option in group["options"])
        assert all(group["options"][index]["bbox"]["x"] < group["options"][index + 1]["bbox"]["x"] for index in range(3))


def test_character_and_line_noise_do_not_create_or_displace_option_groups():
    image = grid_image()
    cv2.putText(image, "123 ABC", (85, 170), cv2.FONT_HERSHEY_SIMPLEX, .7, (0, 0, 0), 1)
    cv2.putText(image, "DEF 567", (95, 520), cv2.FONT_HERSHEY_SIMPLEX, .9, (0, 0, 0), 2)
    cv2.line(image, (80, 580), (670, 580), (0, 0, 0), 1)
    result = ObjectiveGridAnalyzer().analyze(payload(image))
    assert len(result["groups"]) == 6
    assert all(len(group["options"]) == 4 for group in result["groups"])
    assert all(.2 < option["bbox"]["y"] < .43 for group in result["groups"] for option in group["options"])


def test_question_area_outlines_and_inner_letters_do_not_hide_or_duplicate_small_cells():
    image = grid_image()
    for column in range(2):
        for row in range(3):
            left, top = 125 + column * 320, 195 + row * 90
            cv2.rectangle(image, (left, top), (left + 185, top + 50), (0, 0, 0), 1)
            for option in range(4):
                cv2.putText(image, "O", (145 + column * 320 + option * 45, 225 + row * 90),
                            cv2.FONT_HERSHEY_SIMPLEX, .4, (0, 0, 0), 1)
    result = ObjectiveGridAnalyzer().analyze(payload(image))
    assert len(result["groups"]) == 6
    assert all(len(group["options"]) == 4 for group in result["groups"])
    assert all(option["bbox"]["width"] == pytest.approx(29 / 760, abs=2 / 760)
               for group in result["groups"] for option in group["options"])


@pytest.mark.parametrize("count", [2, 12])
def test_minimum_and_maximum_explicit_option_counts_use_only_measured_cells(count):
    image = np.full((1000, 760, 3), 255, np.uint8)
    for option in range(count):
        draw_option(image, (105 + option * 45, 220))
    result = ObjectiveGridAnalyzer().analyze(payload(image, option_count=count))
    assert len(result["groups"]) == 1
    assert len(result["groups"][0]["options"]) == count


@pytest.mark.parametrize("centers, count", [([150, 195, 285], 4), ([150, 195, 270, 315], 4),
                                            ([110 + number * 40 for number in range(8)], 4)])
def test_missing_irregular_or_ambiguous_groups_are_not_completed_by_equal_division(centers, count):
    image = np.full((1000, 760, 3), 255, np.uint8)
    for x in centers:
        draw_option(image, (x, 220))
    result = ObjectiveGridAnalyzer().analyze(payload(image, option_count=count))
    assert result["groups"] == []
    assert "NO_RELIABLE_OPTION_GROUPS" in result["reason_codes"]
    assert set(result["reason_codes"]) & {"OPTION_COUNT_OR_GROUPING_UNCERTAIN", "OPTION_SPACING_OR_SIZE_UNCERTAIN"}


def test_vertical_option_groups_keep_measured_top_to_bottom_order():
    image = np.full((1000, 760, 3), 255, np.uint8)
    for column in range(3):
        for option in range(4):
            draw_option(image, (160 + column * 150, 220 + option * 45), "circle", option == 2)
    result = ObjectiveGridAnalyzer().analyze(payload(image, orientation="vertical"))
    assert len(result["groups"]) == 3
    assert all(len(group["options"]) == 4 for group in result["groups"])
    for group in result["groups"]:
        assert [option["bbox"]["y"] for option in group["options"]] == sorted(option["bbox"]["y"] for option in group["options"])


def test_real_boxes_and_padding_stay_inside_manual_block_and_group_bounds():
    request = payload(grid_image(columns=1, rows=1), bbox={"x": .175, "y": .205, "width": .23, "height": .04})
    result = ObjectiveGridAnalyzer().analyze(request)
    assert len(result["groups"]) == 1
    for group in result["groups"]:
        for inner in [group["bbox"], *[option["bbox"] for option in group["options"]]]:
            outer = result["block_bbox"]
            assert inner["x"] >= outer["x"] and inner["y"] >= outer["y"]
            assert inner["x"] + inner["width"] <= outer["x"] + outer["width"] + 1e-9
            assert inner["y"] + inner["height"] <= outer["y"] + outer["height"] + 1e-9
        first = group["options"][0]["bbox"]
        assert first["x"] == pytest.approx(136 / 760, abs=2 / 760)
        assert first["width"] == pytest.approx(29 / 760, abs=2 / 760)


def test_clipped_option_is_not_replaced_by_an_inferred_cell():
    request = payload(grid_image(columns=1, rows=1), bbox={"x": 150 / 760, "y": .19, "width": .35, "height": .1})
    result = ObjectiveGridAnalyzer().analyze(request)
    assert result["groups"] == []
    assert "NO_RELIABLE_OPTION_GROUPS" in result["reason_codes"]


def test_empty_crop_has_explicit_no_geometry_reason():
    result = ObjectiveGridAnalyzer().analyze(payload(np.full((300, 300, 3), 255, np.uint8)))
    assert result["groups"] == []
    assert "NO_RELIABLE_OPTION_CELLS" in result["reason_codes"]


def open_bracket_grid(missing=None, filled=True):
    image = np.full((1000, 760, 3), 255, np.uint8)
    for column in range(2):
        for row in range(3):
            for option in range(4):
                if (column, row, option) == missing:
                    continue
                x, y = 125 + column * 320 + option * 45, 210 + row * 90
                if filled and option == row % 4:
                    cv2.rectangle(image, (x - 1, y - 1), (x + 27, y + 15), (0, 0, 0), -1)
                    continue
                for edge, direction in [(x, 1), (x + 26, -1)]:
                    cv2.line(image, (edge, y), (edge, y + 10), (0, 0, 0), 2)
                    cv2.line(image, (edge, y), (edge + direction * 5, y), (0, 0, 0), 2)
                    cv2.line(image, (edge, y + 10), (edge + direction * 5, y + 10), (0, 0, 0), 2)
                cv2.putText(image, "ABCD"[option], (x + 8, y + 8), cv2.FONT_HERSHEY_SIMPLEX,
                            .28, (0, 0, 0), 1)
    return image


@pytest.mark.parametrize("filled", [False, True])
def test_open_bracket_options_use_actual_bracket_pairs_and_filled_components(filled):
    request = payload(open_bracket_grid(filled=filled))
    result = ObjectiveGridAnalyzer().analyze(request)
    assert len(result["groups"]) == 6
    assert "OPEN_BRACKET_GROUPS_REQUIRE_REVIEW" in result["reason_codes"]
    for group in result["groups"]:
        assert len(group["options"]) == 4
        assert group["reason_codes"] == ["GEOMETRY_ONLY_REQUIRES_REVIEW"]
        assert all(.15 < option["bbox"]["x"] < .85 for option in group["options"])


def test_open_bracket_missing_option_is_not_filled_from_regular_neighbors():
    result = ObjectiveGridAnalyzer().analyze(payload(open_bracket_grid(missing=(0, 1, 3))))
    assert len(result["groups"]) == 5
    assert not any(group["bbox"]["x"] < .4 and .28 < group["bbox"]["y"] < .33 for group in result["groups"])


def test_open_bracket_red_annotations_do_not_join_or_create_options():
    image = open_bracket_grid()
    cv2.line(image, (120, 205), (290, 220), (255, 0, 0), 2)
    cv2.putText(image, "ABC", (90, 510), cv2.FONT_HERSHEY_SIMPLEX, .9, (255, 0, 0), 2)
    result = ObjectiveGridAnalyzer().analyze(payload(image))
    # 批注覆盖的真实墨迹已经丢失，只能保留其余可证小格，不能从红线恢复原来黑色结构。
    assert 4 <= len(result["groups"]) <= 6
    assert all(group["bbox"]["y"] < .42 for group in result["groups"])


def test_open_bracket_touching_fill_is_not_split_by_expected_option_count():
    image = open_bracket_grid(filled=False)
    cv2.rectangle(image, (125 + 2 * 45 - 4, 210 - 1), (125 + 3 * 45 + 28, 210 + 15), (0, 0, 0), -1)
    result = ObjectiveGridAnalyzer().analyze(payload(image))
    assert len(result["groups"]) == 5
    assert not any(group["bbox"]["x"] < .4 and .18 < group["bbox"]["y"] < .23 for group in result["groups"])


def test_open_bracket_like_letter_cannot_create_an_extra_pair_across_neighboring_cells():
    image = open_bracket_grid(filled=False)
    # 字母扫描后也可能像括号；内部笔画跨接下一个右括号时，不能多生成一格而整题漏掉。
    cv2.rectangle(image, (133, 210), (143, 220), (255, 255, 255), -1)
    cv2.line(image, (134, 210), (134, 220), (0, 0, 0), 2)
    cv2.line(image, (134, 210), (139, 210), (0, 0, 0), 2)
    cv2.line(image, (134, 220), (139, 220), (0, 0, 0), 2)
    result = ObjectiveGridAnalyzer().analyze(payload(image))
    assert len(result["groups"]) == 6
    assert all(len(group["options"]) == 4 for group in result["groups"])


def test_open_bracket_attached_to_filled_neighbor_is_split_only_at_measured_thin_bridge():
    image = open_bracket_grid(filled=False)
    # D 的填涂越界只通过一条细桥碰到 C 的右括号，仍可从像素证明两边各自的形状。
    cv2.rectangle(image, (244, 208), (288, 224), (0, 0, 0), -1)
    cv2.line(image, (241, 215), (245, 215), (0, 0, 0), 1)
    result = ObjectiveGridAnalyzer().analyze(payload(image))
    assert len(result["groups"]) == 6
    row = next(group for group in result["groups"] if group["bbox"]["x"] < .4 and .18 < group["bbox"]["y"] < .23)
    c, d = [option["bbox"] for option in row["options"]][2:]
    assert c["x"] + c["width"] <= d["x"]
    assert c["x"] == pytest.approx(214 / 760, abs=2 / 760)
    assert d["x"] == pytest.approx(244 / 760, abs=2 / 760)


@pytest.mark.parametrize("is_left", [False, True])
def test_attached_bracket_split_preserves_measured_orientation(is_left):
    ink = np.zeros((40, 70), np.uint8)
    cv2.line(ink, (14, 12), (14, 22), 255, 1)
    cv2.line(ink, (10, 12), (14, 12), 255, 1)
    cv2.line(ink, (10, 22), (14, 22), 255, 1)
    cv2.rectangle(ink, (19, 10), (49, 24), 255, -1)
    cv2.line(ink, (14, 17), (19, 17), 255, 1)
    if is_left:
        ink = np.fliplr(ink).copy()
    count, _, stats, _ = cv2.connectedComponentsWithStats(ink, 8)
    assert count == 2
    cell = tuple(map(int, stats[1, :4]))
    parts = _split_attached_bracket(ink, cell)
    assert len(parts) == 2
    assert all(np.count_nonzero(ink[y:y + h, x:x + w]) > 0 for x, y, w, h in parts)


def test_closed_groups_do_not_disable_open_bracket_detection_on_other_rows():
    image = open_bracket_grid()
    cv2.rectangle(image, (120, 205), (292, 232), (255, 255, 255), -1)
    for option in range(4):
        draw_option(image, (138 + option * 45, 219))
    result = ObjectiveGridAnalyzer().analyze(payload(image))
    assert len(result["groups"]) == 6
    assert "OPEN_BRACKET_GROUPS_REQUIRE_REVIEW" in result["reason_codes"]


@pytest.mark.parametrize("change", ["hash", "not_image", "dimensions", "pixels", "bbox", "nan", "boolean",
                                    "count", "orientation", "model", "extra"])
def test_invalid_or_untrusted_input_is_rejected_before_cv(change):
    request = payload()
    if change == "hash":
        request["page"]["sha256"] = "0" * 64
    elif change == "not_image":
        raw = b"private-invalid-image"
        request["page"].update(data_base64=base64.b64encode(raw).decode(), sha256=hashlib.sha256(raw).hexdigest())
    elif change == "dimensions":
        request["page"]["width"] += 1
    elif change == "pixels":
        request["page"].update(width=2048, height=2048)
    elif change == "bbox":
        request["block_bbox"]["width"] = 1
    elif change == "nan":
        request["block_bbox"]["x"] = float("nan")
    elif change == "boolean":
        request["block_bbox"]["x"] = True
    elif change == "count":
        request["option_count"] = 13
    elif change == "orientation":
        request["option_orientation"] = "guess"
    elif change == "model":
        request["managed_model"] = {"api_key": "private-key"}
    else:
        request["question_ids"] = ["invented-question-id"]
    with patch("grading_agent.choice_grid.detect_option_groups") as detect, pytest.raises(AgentError) as caught:
        ObjectiveGridAnalyzer().analyze(request)
    assert caught.value.status == 400
    assert "private" not in json.dumps(caught.value.payload())
    detect.assert_not_called()


def test_authenticated_objective_grid_route_is_independent_of_models_and_grading_idempotency():
    model = FakeModel()
    app = GradingAgentApplication(settings(), model=model)
    server = GradingAgentHTTPServer(("127.0.0.1", 0), app)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        request = urlrequest.Request(f"http://127.0.0.1:{server.server_port}/answer-sheet/objective-grid",
                                     data=json.dumps(payload()).encode(), headers={"Content-Type": "application/json"}, method="POST")
        with pytest.raises(urlerror.HTTPError) as caught:
            urlrequest.urlopen(request, timeout=3)
        assert caught.value.code == 401
        request.add_header("Authorization", f"Bearer {app.settings.service_token}")
        with patch("grading_agent.server.paper_model") as select_model, urlrequest.urlopen(request, timeout=3) as response:
            result = json.loads(response.read())
        assert result["status"] == "review_required" and len(result["groups"]) == 6
        select_model.assert_not_called()
        assert model.calls == []
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=2)
