import io

import cv2
import numpy as np
from page_processing.config import Config
from page_processing.runner import Runner
from PIL import Image


def _png(image: np.ndarray) -> bytes:
    ok, encoded = cv2.imencode(".png", image)
    assert ok
    return encoded.tobytes()


def _template() -> bytes:
    image = np.full((600, 420, 3), 255, np.uint8)
    cv2.rectangle(image, (20, 20), (400, 580), (0, 0, 0), 3)
    for index, y in enumerate(range(90, 550, 70), start=1):
        cv2.putText(image, f"Q{index}", (45, y), cv2.FONT_HERSHEY_SIMPLEX, 0.8, (0, 0, 0), 2)
        cv2.line(image, (110, y - 15), (370, y - 15), (0, 0, 0), 2)
    return _png(image)


class TemplateMatchClient:
    def __init__(self, assets: dict[str, bytes]) -> None:
        self.assets = assets
        self.completed: tuple[str, dict] | None = None

    def download(self, path: str) -> bytes:
        return self.assets[path]

    def complete_template_match(self, run_id: str, payload: dict) -> None:
        self.completed = (run_id, payload)


def test_runner_matches_first_unbound_page_and_returns_ranked_evidence() -> None:
    source = _template()
    wrong_image = np.full((420, 600, 3), 255, np.uint8)
    cv2.putText(wrong_image, "WRONG", (70, 180), cv2.FONT_HERSHEY_SIMPLEX, 2, (0, 0, 0), 4)
    client = TemplateMatchClient({"/source": source, "/right": source, "/wrong": _png(wrong_image)})
    runner = Runner(client, Config("http://api", "demo", "worker", "secret", "worker-1"))
    task = {
        "id": "task-1",
        "lease_token": "lease-1",
        "task_type": "page_template_match",
        "payload": {
            "exam_id": "exam-1",
            "template_match_run_id": "match-1",
            "source_download_url": "/source",
            "source_content_type": "image/png",
            "candidates": [
                {"template_id": "wrong", "template_content_hash": "sha256:wrong", "template_download_url": "/wrong", "template_content_type": "image/png", "template_page_index": 1},
                {"template_id": "right", "template_content_hash": "sha256:right", "template_download_url": "/right", "template_content_type": "image/png", "template_page_index": 1},
            ],
        },
    }

    runner._process_template_match(task)

    assert client.completed is not None
    run_id, result = client.completed
    assert run_id == "match-1"
    assert result["decision"] == "matched"
    assert result["selected_template_id"] == "right"
    assert result["candidates"][0]["template_id"] == "right"


def test_runner_matches_single_png_to_second_page_of_same_locked_template() -> None:
    first_page = np.full((600, 420, 3), 255, np.uint8)
    cv2.putText(first_page, "FRONT PAGE", (35, 170), cv2.FONT_HERSHEY_SIMPLEX, 1.3, (0, 0, 0), 3)
    source = _template()
    second_page = cv2.imdecode(np.frombuffer(source, np.uint8), cv2.IMREAD_COLOR)
    document = io.BytesIO()
    # 同一模板 ID/hash 下包含两页，输入 PNG 仍是文件内第 1 页；不能靠模板 ID 区分正反面。
    Image.fromarray(first_page).save(document, format="TIFF", save_all=True, append_images=[Image.fromarray(second_page)])
    client = TemplateMatchClient({"/source": source, "/template": document.getvalue()})
    runner = Runner(client, Config("http://api", "demo", "worker", "secret", "worker-1"))
    task = {
        "id": "task-2", "lease_token": "lease-2", "task_type": "page_template_match",
        "payload": {
            "template_match_run_id": "match-2", "source_download_url": "/source",
            "source_content_type": "image/png", "page_no": 1,
            "candidates": [
                {"template_id": "same", "template_content_hash": "sha256:same", "template_download_url": "/template", "template_content_type": "image/tiff", "template_page_index": index}
                for index in (1, 2)
            ],
        },
    }

    runner._process_template_match(task)

    assert client.completed is not None
    _, result = client.completed
    assert result["decision"] == "matched"
    assert result["selected_template_id"] == "same"
    assert result["candidates"][0]["template_page_index"] == 2
    assert result["candidates"][0]["geometry_passed"] is True
    assert {item["template_page_index"] for item in result["candidates"]} == {1, 2}
