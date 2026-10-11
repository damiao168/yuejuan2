import json
import unittest
from contextlib import contextmanager
from pathlib import Path

from grading_agent.app import GradingAgentApplication
from grading_agent.model import DashScopeNativeAdapter, LocalLlamaCppAdapter
from grading_agent.provider_adapter import (
    ProviderAdapter,
    ProviderAdapterRegistry,
    build_provider_adapter,
    default_provider_adapter_registry,
)
from helpers import settings, valid_request

FIXTURES = Path(__file__).parent / "fixtures" / "dashscope-native"


class CompleteAdapter:
    @contextmanager
    def session(self, _request_id):
        yield

    def request(self, _grading_request, repair_reason=None):
        return {"repair_reason": repair_reason}

    def request_structured(self, _request_id, _messages, _schema, _name):
        return {}

    def ready(self):
        return True


class IncompleteAdapter:
    def ready(self):
        return True


class ProviderAdapterRegistryTests(unittest.TestCase):
    def test_default_build_enables_local_and_dashscope_native_adapters(self):
        registry = default_provider_adapter_registry()
        self.assertEqual(registry.enabled_types(), ("dashscope_native", "local_llama_cpp"))
        adapter = build_provider_adapter(settings(), registry)
        self.assertIsInstance(adapter, LocalLlamaCppAdapter)
        self.assertIsInstance(adapter, ProviderAdapter)

    def test_registry_builds_dashscope_native_adapter(self):
        adapter = build_provider_adapter(
            settings(
                adapter_type="dashscope_native",
                model_base_url="https://dashscope.aliyuncs.com/api/v1",
                model_api_key="synthetic-key-with-16-characters",
            )
        )
        self.assertIsInstance(adapter, DashScopeNativeAdapter)

    def test_dashscope_adapter_calls_the_native_generation_path(self):
        calls = []

        def transport(url, payload, headers, timeout):
            calls.append((url, payload, headers, timeout))
            return json.loads(FIXTURES.joinpath("response-text.json").read_text(encoding="utf-8"))

        current = settings(
            adapter_type="dashscope_native",
            model_base_url="https://dashscope.aliyuncs.com/api/v1",
            model_api_key="synthetic-key-with-16-characters",
        )
        adapter = DashScopeNativeAdapter(current, transport=transport)
        output = adapter.request(valid_request())

        self.assertEqual(output["suggested_score"], 4)
        self.assertEqual(
            calls[0][0],
            "https://dashscope.aliyuncs.com/api/v1/services/aigc/text-generation/generation",
        )
        self.assertNotIn("compatible-mode", calls[0][0])
        self.assertEqual(calls[0][2]["Authorization"], "Bearer synthetic-key-with-16-characters")

    def test_registry_selects_exact_registered_adapter(self):
        registry = ProviderAdapterRegistry()
        adapter = CompleteAdapter()
        registry.register("fixture_native", lambda _settings: adapter)
        created = registry.create(settings(adapter_type="fixture_native"))
        self.assertIs(created, adapter)

    def test_application_builds_model_from_explicit_registry(self):
        registry = ProviderAdapterRegistry()
        adapter = CompleteAdapter()
        registry.register("fixture_native", lambda _settings: adapter)
        app = GradingAgentApplication(
            settings(adapter_type="fixture_native"),
            adapter_registry=registry,
        )
        self.assertIs(app.model, adapter)

    def test_unknown_adapter_fails_closed(self):
        registry = ProviderAdapterRegistry()
        test_settings = settings(adapter_type='unregistered_native')
        with self.assertRaisesRegex(ValueError, 'not enabled'):
            registry.create(test_settings)

    def test_duplicate_adapter_registration_is_rejected(self):
        registry = ProviderAdapterRegistry()
        registry.register("fixture_native", lambda _settings: CompleteAdapter())
        with self.assertRaisesRegex(ValueError, "already registered"):
            registry.register("fixture_native", lambda _settings: CompleteAdapter())

    def test_incomplete_adapter_is_rejected(self):
        registry = ProviderAdapterRegistry()
        registry.register("fixture_native", lambda _settings: IncompleteAdapter())
        test_settings = settings(adapter_type='fixture_native')
        with self.assertRaisesRegex(TypeError, 'does not implement'):
            registry.create(test_settings)


if __name__ == "__main__":
    unittest.main()
