"""把内部 image_url 消息转换为原生 DashScope 视觉消息，不依赖模板分析包装器。"""

import copy

from .dashscope_native_contract import DASHSCOPE_MULTIMODAL_GENERATION_PATH, DASHSCOPE_TEXT_GENERATION_PATH
from .errors import AgentError


def native_visual_request(base_url, messages, parameters):
    converted, system = [], []
    for message in messages:
        content = message["content"]
        if message["role"] == "system":
            if not isinstance(content, str):
                raise AgentError("invalid_request", "visual system message must be text", status=400)
            system.append(content)
            continue
        parts = []
        for part in content if isinstance(content, list) else [{"type": "text", "text": content}]:
            if part.get("type") == "image_url":
                url = part.get("image_url", {}).get("url", "")
                if not url.startswith("data:image/png;base64,"):
                    raise AgentError("invalid_request", "visual input must be a verified inline PNG", status=400)
                parts.append({"image": url})
            elif part.get("type") == "text":
                parts.append({"text": part["text"]})
            else:
                raise AgentError("invalid_request", "visual message part is unsupported", status=400)
        converted.append({"role": message["role"], "content": parts})
    output_parameters = copy.deepcopy(parameters)
    output_parameters["enable_thinking"] = False
    # 原生视觉接口不支持 OpenAI response_format，schema 由提示词约束且在响应边界严格复核。
    return (base_url.removesuffix(DASHSCOPE_TEXT_GENERATION_PATH) + DASHSCOPE_MULTIMODAL_GENERATION_PATH,
            [{"role": "system", "content": [{"text": "\n".join(system)}]}, *converted], output_parameters)
