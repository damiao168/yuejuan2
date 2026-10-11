# Python 原告警的上下文证据

25 条逐项审读记录：22 条建议平台误报处置，1 条本机预览 HTTP 建议按上下文接受，2 条基线已包含 TLS 修复等待实际复扫。没有操作平台、排除源码或放宽规则。

首次 OCR 测试仅收集失败，原因是本次 runner 选择 importlib 模式而现有测试依赖同目录 test_runner；已保留首次日志，并按正常 prepend 模式重跑，结果以逐条 existing_tests 记录为准。

## a5cf89b8-f3ae-4680-8f63-01958c369345

`python:S125` · `ai-services/grading_agent/paper_compact/anchors.py:22`

说明考试题目分值与题干游戏得分的业务区分，紧接分值正则；没有被注释的可执行代码。

当前源码 SHA256：`7c0280d45533f9411587c5116bf0d46cb9a91c9614707cc801abf3dafa0b6251`。平台处置：pending_root。

```python
    _SECTION_START,
    _SOLUTION_MARKER,
)

_NUMBER = r"\d+(?:\.\d+)?"
# 考试分值必须关联题目标题或分节说明。
# 题干中单独出现的“4分”可能是游戏得分，不是该题的分值。
_QUESTION_MARK = re.compile(
    rf"(?:本(?:小)?题|此题)\s*(?:满分|共|计|为)?\s*[:：]?\s*({_NUMBER})\s*分"
)
_LEADING_MARK = re.compile(rf"^\s*[（(]\s*({_NUMBER})\s*分\s*[）)]")
_SECTION_MARK = re.compile(rf"每\s*(?:小)?题\s*(?:共|计|为)?\s*({_NUMBER})\s*分")
_RUBRIC_HEADING = re.compile(r"^\s*[【\[]?(?:评分标准|评分细则|评分参考|给分点|采分点)[】\]]?\s*[:：]?")
_MARK_CLAUSE = re.compile(rf"^\s*(.+?)\s*(?:可)?得\s*({_NUMBER})\s*分\s*$")
```

## a5251f36-bdc6-4beb-a7cf-019745e959b8

`python:S125` · `ai-services/grading_agent/paper_compact/anchors.py:170`

说明解题步骤编号与递增题号的边界，紧接新题判定；属于自然语言规则说明。

当前源码 SHA256：`7c0280d45533f9411587c5116bf0d46cb9a91c9614707cc801abf3dafa0b6251`。平台处置：pending_root。

```python
            self.current_section_entry = entry
            return
        match = _QUESTION_NUMBER.match(text)
        if match:
            number = int(match.group(1))
            # 编号的解题步骤不是新题目。真正的下一题
            # 会推进试卷中单调递增的题号序列。
            if not self.current or (
                number > self.current_number
                and not (self.in_solution and number <= self.current_number)
            ):
                self._start_question(entry, number)
                return
        if self.current:
```

## b523973f-f539-44bf-87b0-4747ebd41917

`python:S125` · `ai-services/grading_agent/paper_compact/layout.py:181`

解释公式裁剪边距使顶边失真，竖直中心排序更接近基线的原因。

当前源码 SHA256：`27f26e4177784b8849943b9dd84439be5bab766c92363983324052bb7d77791c`。平台处置：pending_root。

```python
    return box[1] + box[3] / 2 if box else float(item[0])


def _geometric_key(item):
    box = _box(item)
    # 公式裁剪刻意留有边距，顶部可能高于所属文本行。
    # 相比顶边，竖直中心更适合用来估计基线。
    return (box[1] + box[3] / 2, box[0], item[0]) if box else (float("inf"), 0, item[0])
```

## c5cc5438-00f5-45c5-a3da-0633b87d17fc

`python:S125` · `ai-services/grading_agent/paper_validation.py:9`

解释缺少明确考试分值时清除模型建议分值的保守条件，紧接允许的文本标记正则。

当前源码 SHA256：`c348d45ea41b46aef2fe900c667eebb859ab1905cda53e79bf4c4dbb490c20d3`。平台处置：pending_root。

```python
import re

from .errors import AgentError
from .paper_schema import FORMULA_SUBJECTS, QUESTION_TYPES, ROLES

# 模型仍可能把应用题中的得分误认为考试分值。
# 这个保守的直接文本检查仅在所有引用文档都没有
# 明确的题目或分节分值标记时，才清除建议分值。
_EXPLICIT_EXAM_MARK = re.compile(
    r"(?:本(?:小)?题|第\s*\d+\s*题)\s*(?:满分|共|计|为)?\s*[:：]?\s*\d+(?:\.\d+)?\s*分"
    r"|每\s*(?:小)?题\s*(?:共|计|为)?\s*\d+(?:\.\d+)?\s*分"
    r"|[（(]\s*\d+(?:\.\d+)?\s*分\s*[）)]"
)

```

## 48bf073b-9633-452f-bfc0-13ca6afaefea

`python:S125` · `ai-services/grading_agent/paper_visual.py:113`

解释旧保存样本回放兼容、紧凑契约展开与供应商流量边界，紧接本地表示转换。

当前源码 SHA256：`79db5147de6c10be006a2ecb6ebff05c5bf378a0b63b9ad483627b62427275da`。平台处置：pending_root。

```python
            completed=len(visual_pages),
            total=len(visual_pages),
            route="visual_model",
            message="原图识别已返回，正在校验页面来源和结构",
        )
        # 旧的已保存测试样本使用持久化表示。保留回放
        # 兼容性不会增加供应商流量；实时请求使用上面的
        # 紧凑契约，并在此按确定规则展开。
        if "question_candidates" not in output:
            output = self._expand_visual_output(output)
        self._normalize_visual_refs(output, documents, request_id)
        result = self._validate(output, request_id, subject, documents)
        usage_reader = getattr(self.model, "last_usage", None)
        if callable(usage_reader):
```

## 47e8a60e-b39d-4658-8498-58cee45877da

`python:S2245` · `scripts/generate-fujian-2024-math-simulation.py:270`

种子驱动的仿真字体颜色、角度、选择题笔画和扫描噪声，不用于密码、令牌、权限、真实考生身份或密钥。改为密码学随机源将破坏可复现的仿真与已有种子契约。

当前源码 SHA256：`1eaf5a87ea828f9e6069cbec7445b62889497720528ba74506becc01970790bd`。平台处置：pending_root。

```python
        return
    rng = random.Random(seed)
    fnt = font(FONT_HAND if seed % 2 == 0 else FONT_KAI, size)
    scratch = Image.new("RGBA", (1100, size * 2 + 30), (255, 255, 255, 0))
    sdraw = ImageDraw.Draw(scratch)
    color = (22 + rng.randint(0, 12), 36 + rng.randint(0, 16), 55 + rng.randint(0, 18), 245)
    sdraw.text((12, 3), text, font=fnt, fill=color)
    angle = rng.uniform(-2.0, 2.0)
    scratch = scratch.rotate(angle, resample=Image.Resampling.BICUBIC, expand=True)
    if blur:
        scratch = scratch.filter(ImageFilter.GaussianBlur(blur))
    canvas.alpha_composite(scratch, xy)


```

## 714b91a4-a04d-48cf-b395-8ea5b285612e

`python:S2245` · `scripts/generate-fujian-2024-math-simulation.py:270`

种子驱动的仿真字体颜色、角度、选择题笔画和扫描噪声，不用于密码、令牌、权限、真实考生身份或密钥。改为密码学随机源将破坏可复现的仿真与已有种子契约。

当前源码 SHA256：`1eaf5a87ea828f9e6069cbec7445b62889497720528ba74506becc01970790bd`。平台处置：pending_root。

```python
        return
    rng = random.Random(seed)
    fnt = font(FONT_HAND if seed % 2 == 0 else FONT_KAI, size)
    scratch = Image.new("RGBA", (1100, size * 2 + 30), (255, 255, 255, 0))
    sdraw = ImageDraw.Draw(scratch)
    color = (22 + rng.randint(0, 12), 36 + rng.randint(0, 16), 55 + rng.randint(0, 18), 245)
    sdraw.text((12, 3), text, font=fnt, fill=color)
    angle = rng.uniform(-2.0, 2.0)
    scratch = scratch.rotate(angle, resample=Image.Resampling.BICUBIC, expand=True)
    if blur:
        scratch = scratch.filter(ImageFilter.GaussianBlur(blur))
    canvas.alpha_composite(scratch, xy)


```

## 93ebff75-53e8-459e-bc8b-e76b2a9dcda5

`python:S2245` · `scripts/generate-fujian-2024-math-simulation.py:270`

种子驱动的仿真字体颜色、角度、选择题笔画和扫描噪声，不用于密码、令牌、权限、真实考生身份或密钥。改为密码学随机源将破坏可复现的仿真与已有种子契约。

当前源码 SHA256：`1eaf5a87ea828f9e6069cbec7445b62889497720528ba74506becc01970790bd`。平台处置：pending_root。

```python
        return
    rng = random.Random(seed)
    fnt = font(FONT_HAND if seed % 2 == 0 else FONT_KAI, size)
    scratch = Image.new("RGBA", (1100, size * 2 + 30), (255, 255, 255, 0))
    sdraw = ImageDraw.Draw(scratch)
    color = (22 + rng.randint(0, 12), 36 + rng.randint(0, 16), 55 + rng.randint(0, 18), 245)
    sdraw.text((12, 3), text, font=fnt, fill=color)
    angle = rng.uniform(-2.0, 2.0)
    scratch = scratch.rotate(angle, resample=Image.Resampling.BICUBIC, expand=True)
    if blur:
        scratch = scratch.filter(ImageFilter.GaussianBlur(blur))
    canvas.alpha_composite(scratch, xy)


```

## da5b8565-5fa8-4f5c-9615-f19c729b4513

`python:S2245` · `scripts/generate-fujian-2024-math-simulation.py:328`

种子驱动的仿真字体颜色、角度、选择题笔画和扫描噪声，不用于密码、令牌、权限、真实考生身份或密钥。改为密码学随机源将破坏可复现的仿真与已有种子契约。

当前源码 SHA256：`1eaf5a87ea828f9e6069cbec7445b62889497720528ba74506becc01970790bd`。平台处置：pending_root。

```python
            draw_centered(draw, (cx, cy - 14), option, font(FONT_SANS, 20), (70, 70, 70))
            if option in selected_options:
                # 每个选项独立取种子并画七笔；合并随机源会改变其他题目的仿真外观。
                rng = random.Random(candidate_index * 100 + index * 9 + option_index)
                for _ in range(7):
                    dx = rng.randint(-13, 13)
                    draw.line((cx - 12, cy + dx // 3, cx + 12, cy + dx), fill=(28, 37, 45), width=3)
        regions[question_no] = {
            "x": x0 / WIDTH,
            "y": (y0 - 20) / HEIGHT,
            "width": 270 / WIDTH,
            "height": 105 / HEIGHT,
        }

```

## 98f898f4-db4f-4c79-80d0-7879c792590d

`python:S2245` · `scripts/generate-fujian-2024-math-simulation.py:448`

种子驱动的仿真字体颜色、角度、选择题笔画和扫描噪声，不用于密码、令牌、权限、真实考生身份或密钥。改为密码学随机源将破坏可复现的仿真与已有种子契约。

当前源码 SHA256：`1eaf5a87ea828f9e6069cbec7445b62889497720528ba74506becc01970790bd`。平台处置：pending_root。

```python
    angle = float(scan["angle"])
    rgb = rgb.rotate(angle, resample=Image.Resampling.BICUBIC, expand=False, fillcolor=(244, 243, 239))
    pixels = rgb.load()
    noise = float(scan["noise"])
    for _ in range(int(WIDTH * HEIGHT * 0.0025)):
        x = rng.randrange(WIDTH)
        y = rng.randrange(HEIGHT)
        r, g, b = pixels[x, y]
        delta = int(rng.gauss(0, noise * 3))
        pixels[x, y] = (
            max(0, min(255, r + delta)),
            max(0, min(255, g + delta)),
            max(0, min(255, b + delta)),
        )
```

## 14fc6509-b17c-4aca-9639-d962135439c3

`python:S2245` · `scripts/generate-fujian-2024-math-simulation.py:449`

种子驱动的仿真字体颜色、角度、选择题笔画和扫描噪声，不用于密码、令牌、权限、真实考生身份或密钥。改为密码学随机源将破坏可复现的仿真与已有种子契约。

当前源码 SHA256：`1eaf5a87ea828f9e6069cbec7445b62889497720528ba74506becc01970790bd`。平台处置：pending_root。

```python
    rgb = rgb.rotate(angle, resample=Image.Resampling.BICUBIC, expand=False, fillcolor=(244, 243, 239))
    pixels = rgb.load()
    noise = float(scan["noise"])
    for _ in range(int(WIDTH * HEIGHT * 0.0025)):
        x = rng.randrange(WIDTH)
        y = rng.randrange(HEIGHT)
        r, g, b = pixels[x, y]
        delta = int(rng.gauss(0, noise * 3))
        pixels[x, y] = (
            max(0, min(255, r + delta)),
            max(0, min(255, g + delta)),
            max(0, min(255, b + delta)),
        )
    if noise >= 3:
```

## 50e8770b-7ffa-48fd-b9bf-1ccd9fccf672

`python:S125` · `services/image-quality-worker/image_quality/geometry.py:197`

解释平板扫描无可见外轮廓时降低置信度的原因，没有虚构边框可靠性。

当前源码 SHA256：`03e0ba6c7c21e4646bbfdb4da281114b893bf4bf774cebb694adf9fa0711e129`。平台处置：pending_root。

```python
    if detected_sides:
        confidence = _clamp(0.2 + 0.15 * detected_sides + 0.2 * completeness)
        status = "complete" if completeness >= 0.8 else "partial"
        return status, confidence, completeness, None

    # 平板扫描中的纸张可能延伸到画布各边，因此
    # 看不到外部轮廓。此时报告中等完整度和低置信度，
    # 而不虚构可靠的边框观测结果。
    edge_band = max(2, int(min(width, height) * 0.015))
    edge_pixels = np.concatenate(
        (
            gray[:edge_band, :].ravel(),
            gray[-edge_band:, :].ravel(),
            gray[:, :edge_band].ravel(),
```

## e5ef3e47-9d26-44f2-aa67-b07f348fcbba

`python:S125` · `services/ocr-worker/ocr_worker/formula_validation.py:70`

解释数学宏白名单及未知宏拒绝的安全边界，紧接允许列表。

当前源码 SHA256：`51ffd42ae6dc7fbb99a3077075ca0f38b38f651fa97a8b1143300fd39815641e`。平台处置：pending_root。

```python
    }
)
_SUPERSCRIPTS = str.maketrans("⁰¹²³⁴⁵⁶⁷⁸⁹", "0123456789")
_SUBSCRIPTS = str.maketrans("₀₁₂₃₄₅₆₇₈₉", "0123456789")

# 这里刻意为初高中印刷数学内容采用较宽的
# 允许列表。未知宏会被拒绝，不会交给可能解释
# 文件或进程命令的渲染器。
_ALLOWED_MACROS = {
    "alpha", "beta", "gamma", "delta", "epsilon", "varepsilon", "zeta", "eta", "theta",
    "vartheta", "iota", "kappa", "lambda", "mu", "nu", "xi", "pi", "varpi", "rho",
    "varrho", "sigma", "varsigma", "tau", "upsilon", "phi", "varphi", "chi", "psi", "omega",
    "Gamma", "Delta", "Theta", "Lambda", "Xi", "Pi", "Sigma", "Upsilon", "Phi", "Psi", "Omega",
    "frac", "sqrt", "overline", "underline", "widehat", "widetilde", "hat", "bar", "vec", "dot", "ddot",
```

## 84a92061-284f-42a2-9701-2c8b6bbe4cb2

`python:S125` · `services/ocr-worker/ocr_worker/math_layout.py:164`

解释行内小公式裁剪扩展同时受配置和检测框高度约束的原因。

当前源码 SHA256：`3d05ca211492c96a33c1ad534ec9cf306a470054187cc22513c328a8432993cb`。平台处置：pending_root。

```python
    max_padding: int = 96,
    max_padding_height_ratio: float = 0.75,
    edge_threshold: float = FORMULA_EDGE_INK_THRESHOLD,
) -> tuple[bytes, list[float], float, int, bool]:
    current_padding = max(0, padding)
    # 对大型独立公式适用的绝对扩展量，可能会
    # 把周围文字纳入小型行内公式。扩展范围应同时
    # 受配置上限和检测框高度约束。
    detected_height = max(1.0, abs(float(bbox[3])))
    proportional_limit = math.ceil(detected_height * max(0.25, max_padding_height_ratio))
    max_padding = max(current_padding, min(max_padding, proportional_limit))
    recrop_count = 0
    while True:
        crop, padded_bbox, edge_ink = crop_formula(image, bbox, current_padding)
```

## 29a146e2-0e55-416b-b7af-fa375edd2c62

`python:S125` · `services/ocr-worker/ocr_worker/paper_formula.py:544`

解释检测分类和边缘裁剪异常不能通过升级识别模型修复，紧接备用模型触发条件。

当前源码 SHA256：`547e2ffc4d91dd3e67d91c72a81fb833444353f88353c0593c91f0408385e2f5`。平台处置：pending_root。

```python
        "reason_codes": list(validation.reason_codes),
    }


def _needs_fallback(primary: Any, detector_score: float, edge_ink: float) -> bool:
    # 检测分数低属于分类问题，边缘有墨迹属于裁剪
    # 问题。更大的识别模型无法修复这两类问题。
    return (
        detector_score >= FORMULA_DETECTOR_ACCEPT_SCORE
        and edge_ink <= FORMULA_EDGE_INK_THRESHOLD
        and not _valid_formula(primary.canonical_latex)
    )


```

## 80edea87-9b8d-41db-a7c9-1a79d1431f39

`python:S125` · `services/ocr-worker/ocr_worker/symbolic_runner.py:90`

解释第二阶段重试规范化用于从第一阶段短暂失败恢复的顺序。

当前源码 SHA256：`13abbbb6d5695c5df85d4c2c3505decb2f406528f225ec75d162b9684c801929`。平台处置：pending_root。

```python

def _normalize_symbolic_formulas(
    formulas: dict[str, dict[str, Any]], verifier: Any, heartbeat: Any, checks: list[dict[str, Any]],
) -> dict[str, dict[str, Any]]:
    normalized_asts: dict[str, dict[str, Any]] = {}
    # 在第二阶段重新规范化，避免第一阶段解析器或服务
    # 暂时不可用后，永久阻断数学验证。
    for formula_id, formula in formulas.items():
        if heartbeat is not None:
            heartbeat.raise_if_failed()
        ast = formula.get("ast")
        if not isinstance(ast, dict):
            result = verifier.normalize(formula.get("canonical_latex") or formula.get("raw_latex") or "")
            ast = result.get("ast")
```

## 4da2ff58-ea55-4887-896e-9493efc2131a

`python:S125` · `services/ocr-worker/ocr_worker/symbolic_runner.py:172`

解释求解集合只保存为约束事实，不能直接证明学生结论完整并计分。

当前源码 SHA256：`13abbbb6d5695c5df85d4c2c3505decb2f406528f225ec75d162b9684c801929`。平台处置：pending_root。

```python
        if len(symbols) != 1:
            continue
        if heartbeat is not None:
            heartbeat.raise_if_failed()
        result = verifier.solve(ast, next(iter(symbols)))
        # 算出一个集合不能证明学生结论完整。
        # 将其保留为约束事实，不作为评分细则的得分依据。
        check = _check(f"sympy-solve-{formula_id}", "constraint", result,
                       formula_id=formula_id, step_id=step["id"])
        check["reason_code"] = result.get("reason_code", "solution_set_computed")
        checks.append(check)


def _symbols(ast: dict[str, Any]) -> set[str]:
```

## 0fbb4e62-ce85-4424-87b4-ff54ab0e5979

`python:S3415` · `services/ocr-worker/tests/test_startup_batch36.py:129`

第一个参数 plan.profile_sha256 是被测 resolve_formula_runtime_plan 的实际输出；第二个参数 hashlib.sha256(raw).hexdigest() 是由固定输入字节独立计算的预期值，符合本文件统一的 actual-first unittest 约定。

当前源码 SHA256：`cd7b9032a685d25b2c39b9a57d96ccaa18ad136af15f4aaed1cbec0005802cde`。平台处置：pending_root。

```python
        stack.enter_context(patch.object(profile, "formula_software_descriptor", return_value=self.software))

    def test_file_profile_keeps_original_byte_digest(self):
        raw = json.dumps(self.payload, indent=2).encode()
        with patch.object(profile.Path, "read_bytes", return_value=raw) as read_bytes:
            plan = profile.resolve_formula_runtime_plan(self.settings)
        read_bytes.assert_called_once_with()
        self.assertEqual((plan.lifecycle, plan.batch_size, plan.source), ("resident", 4, "measured_profile"))
        self.assertEqual(plan.reason, "compatible_measured_profile")
        self.assertEqual(plan.profile_sha256, hashlib.sha256(raw).hexdigest())
        self.assertEqual(plan.hardware_fingerprint, self.payload["hardware_fingerprint"])

    def test_supplied_profile_keeps_canonical_digest_without_file_read(self):
        canonical = json.dumps(self.payload, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode()
```

## 2674ad9e-690b-4a95-a569-05ee335514b7

`python:S125` · `services/page-processing-worker/page_processing/config.py:36`

解释顺序处理且只给当前任务发心跳，批量提前领任务会造成后续租约失效。

当前源码 SHA256：`65e6299c54efbfa79f2479a4ffcc06a0c028364955ae32ac446fec3fd45f74d4`。平台处置：pending_root。

```python

    def __post_init__(self) -> None:
        validate_service_url("EDUGRADE_API_BASE_URL", self.base_url, os.getenv("EDUGRADE_ENV", "development"))
        if not self.tenant_code.strip() or not self.worker_id.strip():
            raise ValueError("tenant_code and worker_id must not be empty")
        # 只有处理任务时才启动心跳。提前领取多个
        # 任务可能导致后续租约在长时间解码期间过期。
        if self.batch_size != 1:
            raise ValueError("batch_size must be 1 for the sequential page-processing worker")
        # 与运行时自身的边界一致（internal/workerruntime/validation.go）：
        # 越界的租约会被静默调整为 300 秒，因此若用
        # 未调整的值校验心跳频率，就会拿服务端
        # 从未授予的租约进行比较。
        validate_lease_timing(self.lease_seconds, self.heartbeat_interval, self.heartbeat_timeout)
```

## 89cc6e21-a251-4e66-8504-04944e1ae32f

`python:S125` · `services/page-processing-worker/page_processing/omr.py:68`

解释空白参考图配准产生窄光晕，测量新增墨迹前去除该光晕的边界。

当前源码 SHA256：`2abd891a47f7d46e4298900f20a62e642c1772e33f10fe8e2c808cca00ab6808`。平台处置：pending_root。

```python
            reference_page_height,
        )
        if reference.shape != image.shape:
            reference = _align_reference_crop(reference, image.shape)
        reference_foreground = _foreground_mask(reference)
        # 配准会在印刷文字和填涂框轮廓周围引入
        # 亚像素抗锯齿。测量学生新增墨迹前，只去除
        # 空白页中很窄的一圈光晕。
        kernel_size = max(1, int(profile.reference_mask_dilation_pixels) * 2 + 1)
        reference_mask = cv2.dilate(reference_foreground, np.ones((kernel_size, kernel_size), dtype=np.uint8))
        binary = cv2.bitwise_and(foreground, cv2.bitwise_not(reference_mask))
    else:
        binary = foreground

```

## 3800c8aa-b10c-4729-8558-c3b6289ae013

`python:S125` · `services/page-processing-worker/page_processing/omr_acceptance.py:84`

解释确定性扫描噪声必须放在答案内部以外，避免验收只处理相同图像副本。

当前源码 SHA256：`895fc941df4007b0a0af4b26e6cac72a858a4b83db2fc440652afed3b84d38ed`。平台处置：pending_root。

```python
            # 内方块刻意设置在模糊阈值与已填涂阈值之间。
            draw.rectangle((x + 11, y + 11, x + 18, y + 18), fill="black")
        else:
            draw.ellipse((x + 6, y + 6, x + width - 6, y + height - 6), fill="black")

    # 在答案内部以外添加确定的低强度扫描噪声，
    # 避免验收流程只处理与单张图像字节完全相同的副本。
    pixels = np.asarray(image).copy()
    seed = sum(ord(char) for char in case.sample_id)
    rng = np.random.default_rng(seed)
    for x, y in rng.integers([0, 0], [180, 70], size=(12, 2)):
        if y < 15 or y > 55:
            pixels[y, x] = (220, 220, 220)
    output = io.BytesIO()
```

## 11d11e79-e1bd-4e27-a449-377dd9f6d43b

`python:S125` · `services/page-processing-worker/page_processing/runner.py:128`

解释失败报告也失败时仍维持工作循环及记录原始失败原因的恢复边界。

当前源码 SHA256：`32d236c9d4fa337d61ac403cd1d9b8e3a05d5f5dcbd0a1330284ad778694041e`。平台处置：pending_root。

```python
                    handler(task, heartbeat)
            except Exception as exc:  # noqa: BLE001 - 每个任务必须隔离处理并报告结果，再继续循环。
                try:
                    self._fail(task, exc)
                except Exception as fail_exc:  # noqa: BLE001 - 租约失效或网关故障后，报告也可能失败。
                    # 租约丢失或网关故障不能导致工作进程循环崩溃：
                    # 租约过期后，运行时会重新分配任务。也要记录
                    # 原始失败，因为报告没有送达，日志就成了
                    # 任务失败原因仅存的记录。
                    print(
                        f"page-processing task {task.get('id')} failed: {type(exc).__name__}: {exc}; "
                        f"fail-report also failed: {type(fail_exc).__name__}: {fail_exc}",
                        file=sys.stderr,
                        flush=True,
```

## 7f939914-9bb6-4005-80a0-4c2efe12eedb

`python:S5332` · `ai-services/grading_agent/server.py:291`

整改基线中已有TLS监听策略，创建监听器前验证证书/环境/布尔opt-in/IPv4字面回环约束，配置证书时用最低TLS1.2包装socket；本智能体未修改该业务源码。是否关闭原HTTP告警以主智能体实际复扫结果为准。

当前源码 SHA256：`62c2b234657f1f29d444968d8ce815647f820b1b8c7b3eb7d6a6db6d617c637e`。平台处置：pending_root。

```python
        if settings.tls_cert_file:
            context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
            context.minimum_version = ssl.TLSVersion.TLSv1_2
            context.load_cert_chain(settings.tls_cert_file, settings.tls_key_file)
            server.socket = context.wrap_socket(server.socket, server_side=True)
        server.serve_forever()
    finally:
        # 证书加载失败或服务退出时也要清理；就绪线程先收到退出信号，再关闭监听。
        server.server_close()
```

## 60b4486b-ab39-42bd-914d-ebe6e345921f

`python:S5332` · `docs/architecture/tools/preview_server.py:36`

静态架构预览与渲染结果回写监听固定 IPv4 回环 127.0.0.1:8766；不是生产阅卷接口。路径允许列表、20MiB上传上限及 resolve 后的 BASE 范围检查在同一文件。当前选择保留本机预览 HTTP，需平台按上下文记录接受，而非声称已升级TLS。

当前源码 SHA256：`72ef85e9f65e34f899ea8dcefb2c14ffdfaa7bc8c40e9a7f4890f586d464e08d`。平台处置：pending_root。

```python
"""仅在回环地址提供架构产物并接收渲染结果，不开放业务目录或任意文件写入。"""
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
import re
from urllib.parse import unquote, urlparse

BASE = Path(__file__).resolve().parents[1]

class Handler(SimpleHTTPRequestHandler):
    def __init__(self, *args, **kwargs):
        super().__init__(*args, directory=str(BASE), **kwargs)

    def log_message(self, *args):
        # 本地预览批量加载图纸时不逐请求打印日志；HTTP 错误仍由响应状态返回。
        pass

    def do_POST(self):
        path = unquote(urlparse(self.path).path)
        if not re.fullmatch(r"/(previews/[0-9A-Za-z_.-]+\.(?:svg|png|html)|evidence/[0-9A-Za-z_.-]+\.json)", path):
            self.send_error(400)
            return
        size = int(self.headers.get("Content-Length", "0"))
        if not 0 < size <= 20 * 1024 * 1024:
            self.send_error(413)
            return
        target = (BASE / path[1:]).resolve()
        if not target.is_relative_to(BASE):
            self.send_error(400)
            return
        target.parent.mkdir(exist_ok=True, parents=True)
        target.write_bytes(self.rfile.read(size))
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b"saved")

if __name__ == "__main__":
    ThreadingHTTPServer(("127.0.0.1", 8766), Handler).serve_forever()
```

## 0cb24e71-bce5-4ed4-b2c5-ca9aac275754

`python:S5332` · `services/math-verification-worker/math_verification_worker/server.py:186`

整改基线中已有TLS监听策略，创建监听器前验证证书/环境/布尔opt-in/IPv4字面回环约束，配置证书时用最低TLS1.2包装socket；本智能体未修改该业务源码。是否关闭原HTTP告警以主智能体实际复扫结果为准。

当前源码 SHA256：`d9d6a02c89cea615738f507e3462e185e4c9992537acdb6b93ce28427f9a4f1f`。平台处置：pending_root。

```python
        if cert_file:
            context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
            context.minimum_version = ssl.TLSVersion.TLSv1_2
            context.load_cert_chain(cert_file, key_file)
            server.socket = context.wrap_socket(server.socket, server_side=True)
        server.serve_forever()
    finally:
        # TLS 初始化失败仍需释放监听端口，避免下一次启动被旧套接字阻塞。
        server.server_close()
```
