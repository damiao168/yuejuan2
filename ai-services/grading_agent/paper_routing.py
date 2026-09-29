"""Pure route-selection and compact-result merge policies."""


def merge_compact_chunks(parsed_chunks, documents):
    merged = {
        "documents": [],
        "question_candidates": [],
        "answer_candidates": [],
        "solution_candidates": [],
        "rubric_candidates": [],
        "issues": [],
    }
    roles_by_source = {}
    for parsed in parsed_chunks:
        for item in parsed["documents"]:
            roles_by_source.setdefault(item["source_id"], []).append(item)
        for collection in (
            "question_candidates",
            "answer_candidates",
            "solution_candidates",
            "rubric_candidates",
            "issues",
        ):
            merged[collection].extend(parsed[collection])

    for document in documents:
        source_id = document["source_id"]
        observations = roles_by_source.get(source_id, [])
        known_roles = {
            item["detected_role"]
            for item in observations
            if item["detected_role"] != "unknown"
        }
        # 同一来源的不同分片可能分别是题目和答案；保留 mixed，避免后一个分片覆盖前者。
        if len(known_roles) > 1 or "mixed" in known_roles:
            role = "mixed"
        elif known_roles:
            role = next(iter(known_roles))
        else:
            role = "unknown"
        confidence = max(
            (
                float(item["role_confidence"])
                for item in observations
                if item["detected_role"] == role
                or (role == "mixed" and item["detected_role"] in known_roles)
            ),
            default=0.5,
        )
        merged["documents"].append(
            {
                "source_id": source_id,
                "detected_role": role,
                "role_confidence": confidence,
            }
        )
    return merged

def obviously_unrelated(documents):
    content = "\n".join(
        str(document.get("content", "")) for document in documents
    ).lower()
    exam_markers = (
        "选择题",
        "填空题",
        "判断题",
        "简答题",
        "计算题",
        "作文题",
        "试题",
        "参考答案",
        "答案：",
        "解析：",
        "评分标准",
        "评分细则",
        "本题",
        "每题",
        "答题",
        "question",
        "answer key",
        "marking scheme",
    )
    if any(marker in content for marker in exam_markers):
        return False
    unrelated_groups = (
        ("会议号", "发起人", "参会时长", "最近入会", "回放", "分享会", "会议"),
        ("订单", "购物车", "收货地址", "实付款", "物流", "商品"),
        ("聊天记录", "朋友圈", "点赞", "关注", "私信"),
        ("航班", "酒店", "行程", "景点", "门票"),
    )
    return any(
        sum(marker in content for marker in group) >= 3
        for group in unrelated_groups
    )
