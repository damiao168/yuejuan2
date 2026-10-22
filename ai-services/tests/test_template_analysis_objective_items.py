import pytest
from grading_agent.template_analysis_objective_items import review_objective_items
from grading_agent.template_analysis_schema import model_output_schema
from jsonschema import Draft202012Validator


def objective_group(count):
    items = []
    for index in range(count):
        top, height = .1 + index * .7 / count, .56 / count
        items.append({
            "printed_question_label": str(index + 1),
            "bbox": {"x": .1, "y": top, "width": .6, "height": height},
            "options": [{"label": name, "bbox": {"x": .2 + option * .2, "y": top,
                                                    "width": .1, "height": height}}
                        for option, name in enumerate("AB")],
        })
    return {"role": "objective_group", "proposed_bbox": {"x": .05, "y": .05, "width": .8, "height": .8},
            "objective_items": items}


@pytest.mark.parametrize("count,valid", [(256, True), (257, False)])
def test_objective_item_count_boundary_matches_schema_go_and_editor_contract(count, valid):
    row = objective_group(count)
    payload = {"objective_question_labels": [str(index + 1) for index in range(count)]}
    schema = model_output_schema([], 1, include_observations=True)["properties"]["candidates"]["items"]["properties"]["objective_items"]
    assert Draft202012Validator(schema).is_valid(row["objective_items"]) is valid
    # 单独验证数量/坐标边界，不把模型观察题号上限混入跨语言数组契约。
    issues = review_objective_items(row, payload, check_observed=False)
    assert issues == ([] if valid else ["MODEL_OBS_OBJECTIVE_ITEMS_INVALID"])
