import math
import os
from dataclasses import dataclass
from urllib.parse import urlsplit

from edugrade_worker_runtime import validate_service_url


def _integer(name, default, minimum, maximum):
    raw = os.getenv(name, str(default))
    try:
        value = int(raw)
    except ValueError as exc:
        raise ValueError(f"{name} must be an integer") from exc
    if value < minimum or value > maximum:
        raise ValueError(f"{name} must be between {minimum} and {maximum}")
    return value


def _float(name, default, minimum, maximum):
    raw = os.getenv(name, str(default))
    try:
        value = float(raw)
    except ValueError as exc:
        raise ValueError(f"{name} must be a number") from exc
    if not math.isfinite(value) or value < minimum or value > maximum:
        raise ValueError(f"{name} must be between {minimum} and {maximum}")
    return value


@dataclass(frozen=True)
class Settings:
    environment: str = "development"
    host: str = "0.0.0.0"
    port: int = 8100
    service_token: str = ""
    model_base_url: str = "http://127.0.0.1:8087/v1"
    model_api_key: str = ""
    model_name: str = "qwen3-4b-q4-k-m"
    model_version: str = "Qwen/Qwen3-4B-GGUF:Q4_K_M"
    provider_key: str = "local"
    deployment_key: str = "local-qwen3-4b-q4-k-m"
    adapter_type: str = "local_llama_cpp"
    deployment_region: str = "on_premise"
    capability_profile: str = "local-pilot-v1"
    prompt_version: str = "subjective-governed-cn-subject-routing-v6"
    model_timeout_seconds: int = 230
    model_ready_timeout_seconds: int = 3
    model_readiness_cache_seconds: int = 10
    model_queue_timeout_seconds: int = 5
    model_max_retries: int = 1
    model_context_tokens: int = 4096
    model_max_output_tokens: int = 384
    model_temperature: float = 0.0
    model_seed: int = 42
    max_request_bytes: int = 262144
    idempotency_ttl_seconds: int = 900
    idempotency_max_entries: int = 256
    contract_root: str = "/app/contracts/grading-agent/v1"
    prompt_root: str = "/app/prompts"
    tls_cert_file: str = ""
    tls_key_file: str = ""

    @classmethod
    def from_env(cls):
        settings = cls(
            environment=os.getenv("EDUGRADE_ENV", "development").strip().lower(),
            host=os.getenv("EDUGRADE_GRADING_AGENT_HOST", "0.0.0.0").strip(),
            port=_integer("EDUGRADE_GRADING_AGENT_PORT", 8100, 1, 65535),
            service_token=os.getenv("EDUGRADE_GRADING_AGENT_TOKEN", ""),
            model_base_url=os.getenv("EDUGRADE_GRADING_MODEL_BASE_URL", "http://127.0.0.1:8087/v1").rstrip("/"),
            model_api_key=os.getenv("EDUGRADE_GRADING_MODEL_API_KEY", ""),
            model_name=os.getenv("EDUGRADE_GRADING_MODEL_NAME", "qwen3-4b-q4-k-m").strip(),
            model_version=os.getenv("EDUGRADE_GRADING_MODEL_VERSION", "Qwen/Qwen3-4B-GGUF:Q4_K_M").strip(),
            provider_key=os.getenv("EDUGRADE_GRADING_PROVIDER_KEY", "local").strip(),
            deployment_key=os.getenv(
                "EDUGRADE_GRADING_DEPLOYMENT_KEY", "local-qwen3-4b-q4-k-m"
            ).strip(),
            adapter_type=os.getenv("EDUGRADE_GRADING_ADAPTER_TYPE", "local_llama_cpp").strip(),
            deployment_region=os.getenv("EDUGRADE_GRADING_DEPLOYMENT_REGION", "on_premise").strip(),
            capability_profile=os.getenv(
                "EDUGRADE_GRADING_CAPABILITY_PROFILE", "local-pilot-v1"
            ).strip(),
            prompt_version=os.getenv("EDUGRADE_GRADING_PROMPT_VERSION", "subjective-governed-cn-subject-routing-v6").strip(),
            model_timeout_seconds=_integer("EDUGRADE_GRADING_MODEL_TIMEOUT_SECONDS", 230, 1, 600),
            model_ready_timeout_seconds=_integer("EDUGRADE_GRADING_MODEL_READY_TIMEOUT_SECONDS", 3, 1, 30),
            model_readiness_cache_seconds=_integer("EDUGRADE_GRADING_MODEL_READINESS_CACHE_SECONDS", 10, 5, 60),
            model_queue_timeout_seconds=_integer("EDUGRADE_GRADING_MODEL_QUEUE_TIMEOUT_SECONDS", 5, 0, 120),
            model_max_retries=_integer("EDUGRADE_GRADING_MODEL_MAX_RETRIES", 1, 0, 1),
            model_context_tokens=_integer("EDUGRADE_GRADING_MODEL_CONTEXT_TOKENS", 4096, 1024, 32768),
            model_max_output_tokens=_integer("EDUGRADE_GRADING_MODEL_MAX_OUTPUT_TOKENS", 384, 128, 4096),
            model_temperature=_float("EDUGRADE_GRADING_MODEL_TEMPERATURE", 0, 0, 2),
            model_seed=_integer("EDUGRADE_GRADING_MODEL_SEED", 42, 0, 2147483647),
            max_request_bytes=_integer("EDUGRADE_GRADING_MAX_REQUEST_BYTES", 262144, 1024, 2097152),
            idempotency_ttl_seconds=_integer("EDUGRADE_GRADING_IDEMPOTENCY_TTL_SECONDS", 900, 60, 86400),
            idempotency_max_entries=_integer("EDUGRADE_GRADING_IDEMPOTENCY_MAX_ENTRIES", 256, 1, 10000),
            contract_root=os.getenv("EDUGRADE_GRADING_CONTRACT_ROOT", "/app/contracts/grading-agent/v1"),
            prompt_root=os.getenv("EDUGRADE_GRADING_PROMPT_ROOT", "/app/prompts"),
            tls_cert_file=os.getenv("EDUGRADE_GRADING_AGENT_TLS_CERT_FILE", "").strip(),
            tls_key_file=os.getenv("EDUGRADE_GRADING_AGENT_TLS_KEY_FILE", "").strip(),
        )
        if (
            len(settings.service_token) < 32
            or settings.service_token != settings.service_token.strip()
            or any(character.isspace() for character in settings.service_token)
        ):
            raise ValueError("EDUGRADE_GRADING_AGENT_TOKEN must contain at least 32 non-whitespace characters")
        if not settings.host:
            raise ValueError("EDUGRADE_GRADING_AGENT_HOST must not be empty")
        if bool(settings.tls_cert_file) != bool(settings.tls_key_file):
            raise ValueError("grading-agent TLS certificate and key must be configured together")
        if settings.environment not in {"", "local", "development", "dev", "test"} and not settings.tls_cert_file:
            raise ValueError("grading-agent TLS certificate and key are required in production-like environments")
        try:
            model_url = urlsplit(settings.model_base_url)
            _ = model_url.port
        except ValueError as exc:
            raise ValueError("EDUGRADE_GRADING_MODEL_BASE_URL must be a valid HTTP(S) URL") from exc
        if (
            model_url.scheme not in {"http", "https"}
            or not model_url.hostname
            or model_url.username is not None
            or model_url.password is not None
            or model_url.query
            or model_url.fragment
        ):
            raise ValueError("EDUGRADE_GRADING_MODEL_BASE_URL must be a valid HTTP(S) URL")
        validate_service_url("EDUGRADE_GRADING_MODEL_BASE_URL", settings.model_base_url, settings.environment)
        identity = {
            "EDUGRADE_GRADING_PROVIDER_KEY": settings.provider_key,
            "EDUGRADE_GRADING_DEPLOYMENT_KEY": settings.deployment_key,
            "EDUGRADE_GRADING_ADAPTER_TYPE": settings.adapter_type,
            "EDUGRADE_GRADING_DEPLOYMENT_REGION": settings.deployment_region,
            "EDUGRADE_GRADING_CAPABILITY_PROFILE": settings.capability_profile,
        }
        if any(
            not value
            or len(value) > 128
            or any(character.isspace() for character in value)
            for value in identity.values()
        ):
            raise ValueError("provider and deployment identity fields must be non-empty bounded identifiers")
        if settings.adapter_type not in {"local_llama_cpp", "dashscope_native"}:
            raise ValueError("EDUGRADE_GRADING_ADAPTER_TYPE is not enabled in this build")
        if settings.adapter_type == "dashscope_native":
            if model_url.scheme != "https" or model_url.hostname != "dashscope.aliyuncs.com":
                raise ValueError(
                    "dashscope_native requires https://dashscope.aliyuncs.com/api/v1"
                )
            if model_url.path.rstrip("/") != "/api/v1":
                raise ValueError(
                    "dashscope_native requires https://dashscope.aliyuncs.com/api/v1"
                )
            if len(settings.model_api_key.strip()) < 16:
                raise ValueError("dashscope_native requires EDUGRADE_GRADING_MODEL_API_KEY")
        if not settings.model_name or not settings.model_version or not settings.prompt_version:
            raise ValueError("model and prompt versions must be configured")
        return settings
