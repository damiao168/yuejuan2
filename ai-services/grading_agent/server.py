import hmac
import json
import ssl
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from .answer_sheet_page_order import AnswerSheetPageOrderAnalyzer
from .app import GradingAgentApplication
from .choice_grid import ObjectiveGridAnalyzer
from .config import Settings
from .errors import AgentError
from .managed_model import paper_model
from .math_rubric_draft import MathRubricDraftGenerator
from .paper_parser import PaperParser
from .template_analysis import TemplateAnalyzer
from .template_analysis_model import prepare_template_model
from .template_analysis_ocr_runtime import get_template_static_ocr_runtime
from .template_matching import TemplateMatcher

PAPER_PARSE_PATH = "/paper/parse"
PAPER_RUBRIC_DRAFT_PATH = "/paper/rubric-draft"
ANSWER_SHEET_ANALYZE_PATH = "/answer-sheet/analyze"
ANSWER_SHEET_MATCH_PATH = "/answer-sheet/match"
ANSWER_SHEET_OBJECTIVE_GRID_PATH = "/answer-sheet/objective-grid"
ANSWER_SHEET_PAGE_ORDER_PATH = "/answer-sheet/page-order"
GRADE_V2_PATH = "/grading/grade-v2"
GRADE_IMAGE_PATH = "/grading/grade-image-v3"
OBSERVE_IMAGE_PATH = "/grading/observe-image-v3"
PROBE_IMAGE_PATH = "/grading/probe-image-v3"


class GradingAgentHTTPServer(ThreadingHTTPServer):
    daemon_threads = True
    allow_reuse_address = True

    def __init__(self, address, application):
        super().__init__(address, GradingAgentHandler)
        self.application = application
        self._readiness_stop = threading.Event()
        self.application.refresh_readiness()
        self._readiness_thread = threading.Thread(
            target=self._refresh_readiness,
            name="grading-agent-readiness",
            daemon=True,
        )
        self._readiness_thread.start()

    def _refresh_readiness(self):
        interval = self.application.settings.model_readiness_cache_seconds
        while not self._readiness_stop.wait(interval):
            self.application.refresh_readiness()

    def server_close(self):
        self._readiness_stop.set()
        if self._readiness_thread is not threading.current_thread():
            self._readiness_thread.join(timeout=1)
        super().server_close()


class GradingAgentHandler(BaseHTTPRequestHandler):
    server_version = "EduGradeGradingAgent/1"

    def do_GET(self):
        if self.path == "/grading/capabilities":
            try:
                self._authorize()
                from .material_runtime import math_wrapper_hash
                settings = self.server.application.settings
                enabled = settings.adapter_type == "local_llama_cpp" and bool(settings.material_callback_base_url)
                self._json(200, {"features": ["material-input-v1"] if enabled else [], "math_wrapper_hash": math_wrapper_hash(),
                                 "max_reference_images": 4, "adapter": settings.adapter_type, "provider": settings.provider_key,
                                 "deployment": settings.deployment_key, "region": settings.deployment_region})
            except AgentError as exc:
                self._error(exc)
            return
        if self.path == "/grading/prompts/current":
            try:
                self._authorize()
                self._json(200, {"prompt": self.server.application.prompt_snapshot()})
            except AgentError as exc:
                self._error(exc)
            return
        if self.path == "/health":
            self._json(
                200,
                {
                    "service": "grading-agent",
                    "status": "healthy",
                    "mode": "shadow",
                    "capability_profile": self.server.application.matrix.profile_id,
                },
            )
            return
        if self.path == "/ready":
            ready = self.server.application.readiness()
            self._json(
                200 if ready else 503,
                {
                    "service": "grading-agent",
                    "status": "ready" if ready else "not_ready",
                    "model_runtime": "available" if ready else "unavailable",
                },
            )
            return
        self._error(AgentError("invalid_request", "route not found", status=404))

    def do_POST(self):
        if self.path not in {"/grading/grade", GRADE_V2_PATH, GRADE_IMAGE_PATH, OBSERVE_IMAGE_PATH, PROBE_IMAGE_PATH, PAPER_PARSE_PATH, PAPER_RUBRIC_DRAFT_PATH, ANSWER_SHEET_ANALYZE_PATH, ANSWER_SHEET_MATCH_PATH, ANSWER_SHEET_OBJECTIVE_GRID_PATH, ANSWER_SHEET_PAGE_ORDER_PATH}:
            self._error(AgentError("invalid_request", "route not found", status=404))
            return
        try:
            self._authorize()
            payload = self._read_json()
            self._dispatch_post(payload)
        except AgentError as exc:
            self._error(exc)
        except Exception as exc:  # noqa: BLE001 - HTTP 边界必须把意外失败转换为安全响应。
            print(
                json.dumps(
                    {
                        "event": "grading_agent_unhandled_error",
                        "error_type": type(exc).__name__,
                    },
                    separators=(",", ":"),
                ),
                file=sys.stderr,
                flush=True,
            )
            self._error(AgentError("internal_error", "grading-agent operation failed", status=500))

    def _dispatch_post(self, payload):
        if self.path == ANSWER_SHEET_PAGE_ORDER_PATH:
            model = None
            if isinstance(payload, dict) and payload.get("managed_model") is not None:
                model = prepare_template_model(paper_model(self.server.application, payload))
            self._json(200, AnswerSheetPageOrderAnalyzer(model).analyze(payload))
            return
        if self.path == ANSWER_SHEET_OBJECTIVE_GRID_PATH:
            # 此入口只解析人工指定大框的实测小格，不能借用聊天或评分模型推断题号映射。
            self._json(200, ObjectiveGridAnalyzer().analyze(payload))
            return
        if self.path == ANSWER_SHEET_MATCH_PATH:
            self._json(200, TemplateMatcher().match(payload))
            return
        if self.path == ANSWER_SHEET_ANALYZE_PATH:
            # 未配置文档模型时仍可返回几何候选；不借用评分模型冒充语义分析成功。
            model = None
            if isinstance(payload, dict):
                if payload.get("managed_model") is not None:
                    model = prepare_template_model(paper_model(self.server.application, payload))
                else:
                    payload.pop("managed_model", None)
            self._json(200, TemplateAnalyzer(model).analyze(payload))
            return
        if self.path == PAPER_PARSE_PATH:
            parser = PaperParser(paper_model(self.server.application, payload))
            if "application/x-ndjson" in self.headers.get("Accept", "").lower():
                self._paper_parse_stream(parser, payload)
                return
            self._json(200, parser.parse(payload))
            return
        if self.path == PAPER_RUBRIC_DRAFT_PATH:
            self._math_rubric_draft(payload)
            return
        self._grade(payload)

    def _math_rubric_draft(self, payload):
        request_id = payload.get("request_id", "") if isinstance(payload, dict) else ""
        if not isinstance(payload, dict) or set(payload) - {
            "request_id", "subject", "question_candidate", "answer_candidate", "solution_candidate", "managed_model",
            "question_reference"
        } or payload.get("subject") not in {"数学", "mathematics"}:
            raise AgentError("invalid_request", "math rubric draft request is invalid", status=400, request_id=request_id)
        if not isinstance(request_id, str) or not 8 <= len(request_id) <= 128:
            raise AgentError("invalid_request", "request_id is invalid", status=400)
        draft = MathRubricDraftGenerator(paper_model(self.server.application, payload)).generate(
            request_id,
            payload.get("question_candidate"),
            payload.get("answer_candidate"),
            payload.get("solution_candidate"),
            question_reference=payload.get("question_reference"),
        )
        self._json(200, draft)

    def _grade(self, payload):
        request_id = payload.get("request_id", "") if isinstance(payload, dict) else ""
        # 阅卷路由必须保留幂等键；试卷解析和评分细则草稿不共用这条重放边界。
        idempotency_key = self.headers.get("Idempotency-Key", "").strip()
        if not idempotency_key:
            raise AgentError(
                "invalid_request",
                "Idempotency-Key header is required",
                status=400,
                request_id=request_id,
            )
        if self.path == PROBE_IMAGE_PATH:
            suggestion, replayed = self.server.application.image_v3.probe(payload, idempotency_key)
        elif self.path in {GRADE_IMAGE_PATH, OBSERVE_IMAGE_PATH}:
            suggestion, replayed = self.server.application.image_v3.execute(
                payload, idempotency_key, "observe" if self.path == OBSERVE_IMAGE_PATH else "grade")
        elif self.path == GRADE_V2_PATH:
            suggestion, replayed = self.server.application.grade_v2(
                payload,
                idempotency_key,
                body_size=int(self.headers.get("Content-Length", "0")),
                content_encoding=self.headers.get("Content-Encoding", ""),
            )
        else:
            suggestion, replayed = self.server.application.grade(payload, idempotency_key)
        self._json(200, suggestion, extra_headers={"Idempotent-Replay": "true" if replayed else "false"})

    def _paper_parse_stream(self, parser, payload):
        """Stream factual parser milestones; a heartbeat is not completion."""

        self.send_response(200)
        self.send_header("Content-Type", "application/x-ndjson; charset=utf-8")
        self.send_header("Cache-Control", "no-store")
        self.send_header("X-Content-Type-Options", "nosniff")
        self.send_header("Connection", "close")
        self.end_headers()
        self.close_connection = True

        def emit(event_type, value):
            line = json.dumps(
                {"type": event_type, event_type: value},
                ensure_ascii=False,
                separators=(",", ":"),
            ).encode("utf-8") + b"\n"
            self.wfile.write(line)
            self.wfile.flush()

        try:
            result = parser.parse(payload, progress=lambda value: emit("progress", value))
            # 流式 HTTP 200 只表示连接已建立，只有 result 事件代表完整解析成功。
            emit("result", result)
        except AgentError as exc:
            print(
                json.dumps(
                    {
                        "event": "grading_agent_paper_parse_rejected",
                        "request_id": exc.request_id,
                        "error_code": exc.code,
                        "error_message": exc.message,
                    },
                    ensure_ascii=False,
                    separators=(",", ":"),
                ),
                file=sys.stderr,
                flush=True,
            )
            emit("error", {"status": exc.status, **exc.payload()})
        except (BrokenPipeError, ConnectionResetError):
            return
        except Exception as exc:  # noqa: BLE001 - 流式响应已经发送 HTTP 响应头。
            print(
                json.dumps(
                    {
                        "event": "grading_agent_paper_stream_error",
                        "error_type": type(exc).__name__,
                    },
                    separators=(",", ":"),
                ),
                file=sys.stderr,
                flush=True,
            )
            try:
                emit(
                    "error",
                    AgentError(
                        "internal_error",
                        "grading-agent operation failed",
                        status=500,
                    ).payload(),
                )
            except (BrokenPipeError, ConnectionResetError):
                return

    def _authorize(self):
        expected = self.server.application.settings.service_token
        supplied = self.headers.get("Authorization", "")
        prefix = "Bearer "
        candidate = supplied[len(prefix) :] if supplied.startswith(prefix) else ""
        if not candidate or not hmac.compare_digest(candidate, expected):
            raise AgentError("unauthorized", "valid service authentication is required", status=401)

    def _read_json(self):
        content_type = self.headers.get("Content-Type", "").split(";", 1)[0].strip().lower()
        if content_type != "application/json":
            raise AgentError("invalid_request", "Content-Type must be application/json", status=415)
        raw_length = self.headers.get("Content-Length", "")
        try:
            length = int(raw_length)
        except ValueError as exc:
            raise AgentError("invalid_request", "Content-Length is required", status=411) from exc
        if self.path == ANSWER_SHEET_PAGE_ORDER_PATH:
            max_bytes = 20 * 1024 * 1024
        elif self.path == ANSWER_SHEET_OBJECTIVE_GRID_PATH:
            max_bytes = 12 * 1024 * 1024
        elif self.path in {GRADE_V2_PATH, GRADE_IMAGE_PATH, OBSERVE_IMAGE_PATH, PROBE_IMAGE_PATH}:
            max_bytes = 8 * 1024 * 1024
        elif self.path in {PAPER_PARSE_PATH, ANSWER_SHEET_ANALYZE_PATH, ANSWER_SHEET_MATCH_PATH}:
            max_bytes = max(
                self.server.application.settings.max_request_bytes,
                48 * 1024 * 1024,
            )
        elif self.path == PAPER_RUBRIC_DRAFT_PATH:
            max_bytes = min(self.server.application.settings.max_request_bytes, 2 * 1024 * 1024)
        else:
            max_bytes = self.server.application.settings.max_request_bytes
        if length <= 0 or length > max_bytes:
            raise AgentError("invalid_request", "request body size is invalid", status=413)
        body = self.rfile.read(length)
        try:
            return json.loads(body.decode("utf-8"))
        except (UnicodeDecodeError, json.JSONDecodeError) as exc:
            raise AgentError("invalid_request", "request body must be valid UTF-8 JSON", status=400) from exc

    def _error(self, error):
        # HTTP 路由决定错误协议版本；授权和读包失败也必须保持 v2 契约。
        schema_version = "grading-image-v3" if self.path in {GRADE_IMAGE_PATH, OBSERVE_IMAGE_PATH, PROBE_IMAGE_PATH} else "grading-agent-v2" if self.path == GRADE_V2_PATH else "grading-agent-v1"
        self._json(error.status, error.payload(schema_version))

    def _json(self, status, payload, extra_headers=None):
        body = json.dumps(payload, ensure_ascii=False, separators=(",", ":")).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Cache-Control", "no-store")
        self.send_header("X-Content-Type-Options", "nosniff")
        for name, value in (extra_headers or {}).items():
            self.send_header(name, value)
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, _format, *_args):
        return


def serve(settings=None):
    settings = settings or Settings.from_env()
    # 调用方直接传入 Settings 仍须检查监听边界，拒绝发生在加载应用和绑定端口之前。
    settings._validate_credentials_and_listener()
    application = GradingAgentApplication(settings)
    server = GradingAgentHTTPServer((settings.host, settings.port), application)
    static_ocr = get_template_static_ocr_runtime()
    try:
        if settings.tls_cert_file:
            context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
            context.minimum_version = ssl.TLSVersion.TLSv1_2
            context.load_cert_chain(settings.tls_cert_file, settings.tls_key_file)
            server.socket = context.wrap_socket(server.socket, server_side=True)
        # 独立静态 OCR 冷启动较慢，后台预热不阻塞健康检查；未就绪时只保留原候选。
        static_ocr.start()
        server.serve_forever()
    finally:
        # 证书加载失败或服务退出时也要清理；就绪线程先收到退出信号，再关闭监听。
        server.server_close()
        static_ocr.close()
