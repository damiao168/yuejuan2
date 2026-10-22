import copy

import pytest

from scripts.evals.answer_sheet_regions import evaluate


def _region(role="answer", question="15", width=.5):
    return {"role": role, "printed_question_label": question,
            "bbox": {"x": .1, "y": .1, "width": width, "height": .7}}


def _corpus(truth, prediction):
    return {"documents": [{"template_family": "synthetic-math", "split": "test",
                           "pages": [{"ground_truth": truth, "predictions": prediction}]}]}


def test_wrong_class_with_perfect_box_is_an_error():
    result = evaluate(_corpus([_region()], [_region("objective_group", None)]))
    assert result["matched_geometry_mean"]["iou"] == 1
    assert result["per_role"]["answer"]["fn"] == 1
    assert result["per_role"]["objective_group"]["fp"] == 1
    assert result["question_mapping_recall"] == 0


def test_missing_edge_is_measured_even_when_iou_matches():
    result = evaluate(_corpus([_region()], [_region(width=.45)]))
    assert result["question_mapping_recall"] == 1
    assert result["matched_geometry_mean"]["missed_writable_fraction"] == pytest.approx(.1)


def test_duplicate_predictions_do_not_raise_recall():
    result = evaluate(_corpus([_region()], [_region(), _region()]))
    assert result["per_role"]["answer"] == {"tp": 1, "fp": 1, "fn": 0, "precision": .5, "recall": 1, "f1": 2 / 3}


def test_same_template_family_cannot_be_split_into_train_and_test():
    corpus = _corpus([_region()], [_region()])
    duplicate = copy.deepcopy(corpus["documents"][0])
    duplicate["split"] = "train"
    corpus["documents"].append(duplicate)
    with pytest.raises(ValueError, match="leaked"):
        evaluate(corpus)


def test_unmatched_supported_prediction_is_counted_as_wrong():
    actual = _region("identity", None)
    actual["semantic_check"] = "supported"
    result = evaluate(_corpus([], [actual]))
    assert result["counts"]["wrong_supported_predictions"] == 1
    assert result["matched_geometry_mean"]["iou"] is None


def test_identity_fields_are_distinct_categories_even_when_role_and_box_match():
    expected, actual = _region("identity", None), _region("identity", None)
    expected["identity_label"], actual["identity_label"] = "姓名", "条码"
    actual["semantic_check"] = "supported"
    result = evaluate(_corpus([expected], [actual]))
    assert result["per_role"]["identity"]["tp"] == 1
    assert result["per_category"]["identity:姓名"]["fn"] == 1
    assert result["per_category"]["identity:条码"]["fp"] == 1
    assert result["counts"]["wrong_supported_predictions"] == 1
