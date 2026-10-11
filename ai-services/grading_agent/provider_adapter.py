import re
from collections.abc import Callable
from typing import Protocol, runtime_checkable

_ADAPTER_TYPE = re.compile(r"^[a-z0-9][a-z0-9._-]{0,127}$")


@runtime_checkable
class ProviderAdapter(Protocol):
    """Minimal execution boundary shared by local and native provider adapters."""

    def session(self, request_id):
        ...

    def request(self, grading_request, repair_reason=None):
        ...

    def request_structured(self, request_id, messages, schema, name):
        ...

    def ready(self):
        ...


AdapterFactory = Callable[[object], ProviderAdapter]


class ProviderAdapterRegistry:
    """Explicit allowlist of adapter implementations available in this build."""

    def __init__(self):
        self._factories: dict[str, AdapterFactory] = {}

    def register(self, adapter_type, factory):
        key = str(adapter_type).strip()
        if not _ADAPTER_TYPE.fullmatch(key):
            raise ValueError("adapter type must be a bounded identifier")
        if key in self._factories:
            raise ValueError(f"adapter type is already registered: {key}")
        if not callable(factory):
            raise TypeError("adapter factory must be callable")
        self._factories[key] = factory

    def create(self, settings):
        adapter_type = str(settings.adapter_type).strip()
        factory = self._factories.get(adapter_type)
        if factory is None:
            raise ValueError(f"adapter type is not enabled in this build: {adapter_type}")
        adapter = factory(settings)
        if not isinstance(adapter, ProviderAdapter):
            raise TypeError(f"adapter does not implement ProviderAdapter: {adapter_type}")
        return adapter

    def enabled_types(self):
        return tuple(sorted(self._factories))


def default_provider_adapter_registry():
    # 保留局部导入，让协议不依赖任何供应商运行时。
    from .model import DashScopeNativeAdapter, LocalLlamaCppAdapter

    registry = ProviderAdapterRegistry()
    registry.register("local_llama_cpp", LocalLlamaCppAdapter)
    registry.register("dashscope_native", DashScopeNativeAdapter)
    return registry


def build_provider_adapter(settings, registry=None):
    active_registry = registry or default_provider_adapter_registry()
    return active_registry.create(settings)
