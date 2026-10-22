"""图像阅卷沿用受控诊断目录和保留期限；普通日志只记录版本、摘要和固定错误码。"""

import time
import uuid

from .template_analysis_diagnostics import AnalysisTrace


class ImageGradingTrace(AnalysisTrace):
    def __init__(self, request, operation, model):
        # 只存助手响应；请求中的图片、标准答案和模型连接凭据不能进入诊断原文。
        self.metadata = {
            "event": "image_grading", "run_id": uuid.uuid4().hex,
            "request_id": request["request_id"], "input_version_id": request["input_version_id"],
            "source_sha256": request.get("student_image", {}).get("sha256", ""),
            "operation": operation, "versions": {"protocol": request["schema_version"]},
            "model_name": str(getattr(getattr(model, "settings", None), "model_name", ""))[:128],
            "attempts": [],
        }
        self._started = time.monotonic()
        self._content, self._content_bytes, self._finished = [], 0, False

    def transport(self, original):
        def capture(*args, **kwargs):
            response = original(*args, **kwargs)
            # 必须在 JSON/schema 校验之前保存响应，否则格式失败后仍无法复现。
            self.capture(response, provider=True)
            return response
        return capture


class ImageProbeTrace(ImageGradingTrace):
    def __init__(self, request_id, model):
        # 探测只有合成色块，没有学生输入；沿用响应采集和受控目录，不记录托管连接或请求图。
        super().__init__({"request_id": request_id, "input_version_id": "", "schema_version": "grading-image-v3"}, "probe", model)
        self.metadata["event"] = "image_model_probe"
