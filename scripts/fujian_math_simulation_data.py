"""福建数学流程仿真的固定题例与手工预期；不代表 OCR 或模型评分质量。"""

from __future__ import annotations

from typing import Any

CHOICE_KEY = ["D", "C", "C", "A", "B", "B", "A", "A", "B", "C"]


FACTORED_ANSWER = "x(x+1)"


COORDINATE_ANSWER = "(2,1)"


FILL_KEY = [FACTORED_ANSWER, "x<1", "90", "2", COORDINATE_ANSWER, "128"]


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
            "Q11": answer(FACTORED_ANSWER, 0.96, "auto_confirm", score=4),
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
            "Q11": answer(FACTORED_ANSWER, 0.72, "rule_grade_review", score=4),
            "Q12": answer("x<1", 0.95, "auto_confirm", score=4),
            "Q13": answer("90", 0.96, "auto_confirm", score=4),
            "Q14": answer("", 0.2, "rule_grade_review", score=0),
            "Q15": answer(COORDINATE_ANSWER, 0.93, "auto_confirm", score=4),
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
            "Q15": answer(COORDINATE_ANSWER, 0.91, "auto_confirm", score=4),
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
