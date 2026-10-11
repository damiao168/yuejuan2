import ssl
from unittest.mock import MagicMock

import pytest
from math_verification_worker import server


@pytest.fixture(autouse=True)
def clean_listener_environment(monkeypatch) -> None:
    for name in (
        "EDUGRADE_ENV",
        "EDUGRADE_MATH_VERIFY_PORT",
        "EDUGRADE_MATH_VERIFY_HOST",
        "EDUGRADE_MATH_VERIFY_ALLOW_INSECURE_LOOPBACK_HTTP",
        "EDUGRADE_MATH_VERIFY_TLS_CERT_FILE",
        "EDUGRADE_MATH_VERIFY_TLS_KEY_FILE",
    ):
        monkeypatch.delenv(name, raising=False)


def test_production_requires_tls_certificate(monkeypatch) -> None:
    monkeypatch.setenv("EDUGRADE_ENV", "production")
    monkeypatch.delenv("EDUGRADE_MATH_VERIFY_TLS_CERT_FILE", raising=False)
    monkeypatch.delenv("EDUGRADE_MATH_VERIFY_TLS_KEY_FILE", raising=False)

    with pytest.raises(ValueError, match="TLS certificate"):
        server.main()


def test_tls_certificate_requires_matching_key(monkeypatch) -> None:
    monkeypatch.setenv("EDUGRADE_ENV", "development")
    monkeypatch.setenv("EDUGRADE_MATH_VERIFY_TLS_CERT_FILE", "server.crt")
    monkeypatch.delenv("EDUGRADE_MATH_VERIFY_TLS_KEY_FILE", raising=False)

    with pytest.raises(ValueError, match="configured together"):
        server.main()


def test_tls_wraps_listener_before_serving(monkeypatch) -> None:
    monkeypatch.setenv("EDUGRADE_ENV", "production")
    monkeypatch.setenv("EDUGRADE_MATH_VERIFY_TLS_CERT_FILE", "server.crt")
    monkeypatch.setenv("EDUGRADE_MATH_VERIFY_TLS_KEY_FILE", "server.key")
    listener = MagicMock()
    original_socket = listener.socket
    context = MagicMock()
    context_type = MagicMock(return_value=context)
    monkeypatch.setattr(server, "ThreadingHTTPServer", MagicMock(return_value=listener))
    monkeypatch.setattr(server.ssl, "SSLContext", context_type)

    server.main()

    context_type.assert_called_once_with(ssl.PROTOCOL_TLS_SERVER)
    assert context.minimum_version == ssl.TLSVersion.TLSv1_2
    context.load_cert_chain.assert_called_once_with("server.crt", "server.key")
    context.wrap_socket.assert_called_once_with(original_socket, server_side=True)
    assert listener.socket is context.wrap_socket.return_value
    listener.serve_forever.assert_called_once_with()
    listener.server_close.assert_called_once_with()


@pytest.mark.parametrize("environment", [None, "", "  ", "unknown", "production", "staging", "preproduction"])
def test_non_development_environment_rejects_cleartext_before_bind(monkeypatch, environment) -> None:
    if environment is not None:
        monkeypatch.setenv("EDUGRADE_ENV", environment)
    monkeypatch.setenv("EDUGRADE_MATH_VERIFY_HOST", "127.0.0.1")
    monkeypatch.setenv("EDUGRADE_MATH_VERIFY_ALLOW_INSECURE_LOOPBACK_HTTP", "true")
    listener_type = MagicMock()
    monkeypatch.setattr(server, "ThreadingHTTPServer", listener_type)

    with pytest.raises(ValueError, match="TLS certificate"):
        server.main()

    listener_type.assert_not_called()


@pytest.mark.parametrize("environment", ["local", "development", "dev", "test"])
@pytest.mark.parametrize("opt_in", [None, "false", " FALSE "])
def test_development_requires_explicit_cleartext_opt_in(monkeypatch, environment, opt_in) -> None:
    monkeypatch.setenv("EDUGRADE_ENV", environment)
    monkeypatch.setenv("EDUGRADE_MATH_VERIFY_HOST", "127.0.0.1")
    if opt_in is not None:
        monkeypatch.setenv("EDUGRADE_MATH_VERIFY_ALLOW_INSECURE_LOOPBACK_HTTP", opt_in)
    listener_type = MagicMock()
    monkeypatch.setattr(server, "ThreadingHTTPServer", listener_type)

    with pytest.raises(ValueError, match="explicit insecure loopback opt-in"):
        server.main()

    listener_type.assert_not_called()


@pytest.mark.parametrize("environment", ["local", "development", "dev", "test", " Development "])
@pytest.mark.parametrize("host", ["127.0.0.1", "127.12.34.56"])
def test_explicit_development_cleartext_binds_only_literal_ipv4_loopback(monkeypatch, environment, host) -> None:
    monkeypatch.setenv("EDUGRADE_ENV", environment)
    monkeypatch.setenv("EDUGRADE_MATH_VERIFY_HOST", host)
    monkeypatch.setenv("EDUGRADE_MATH_VERIFY_PORT", "8123")
    monkeypatch.setenv("EDUGRADE_MATH_VERIFY_ALLOW_INSECURE_LOOPBACK_HTTP", " TRUE ")
    listener = MagicMock()
    listener_type = MagicMock(return_value=listener)
    context_type = MagicMock()
    monkeypatch.setattr(server, "ThreadingHTTPServer", listener_type)
    monkeypatch.setattr(server.ssl, "SSLContext", context_type)

    server.main()

    listener_type.assert_called_once_with((host, 8123), server.Handler)
    context_type.assert_not_called()
    listener.serve_forever.assert_called_once_with()
    listener.server_close.assert_called_once_with()


@pytest.mark.parametrize(
    "host",
    [
        "", " ", "0.0.0.0", "localhost", "math-verification-worker", "10.0.0.2", "128.0.0.1",
        "::1", "::ffff:127.0.0.1", "127.1", "2130706433", "127.000.0.1", "[127.0.0.1]", "127.0.0.1:8092",
    ],
)
def test_cleartext_rejects_non_literal_ipv4_loopback_before_bind(monkeypatch, host) -> None:
    monkeypatch.setenv("EDUGRADE_ENV", "development")
    monkeypatch.setenv("EDUGRADE_MATH_VERIFY_HOST", host)
    monkeypatch.setenv("EDUGRADE_MATH_VERIFY_ALLOW_INSECURE_LOOPBACK_HTTP", "true")
    listener_type = MagicMock()
    monkeypatch.setattr(server, "ThreadingHTTPServer", listener_type)

    with pytest.raises(ValueError, match="HOST must not be empty|literal IPv4 loopback"):
        server.main()

    listener_type.assert_not_called()


@pytest.mark.parametrize("opt_in", ["", " ", "1", "0", "yes", "no", "on", "off", "truthy"])
@pytest.mark.parametrize("with_certificate", [False, True])
def test_invalid_cleartext_boolean_is_rejected_before_bind(monkeypatch, opt_in, with_certificate) -> None:
    monkeypatch.setenv("EDUGRADE_ENV", "test")
    monkeypatch.setenv("EDUGRADE_MATH_VERIFY_HOST", "127.0.0.1")
    monkeypatch.setenv("EDUGRADE_MATH_VERIFY_ALLOW_INSECURE_LOOPBACK_HTTP", opt_in)
    if with_certificate:
        monkeypatch.setenv("EDUGRADE_MATH_VERIFY_TLS_CERT_FILE", "server.crt")
        monkeypatch.setenv("EDUGRADE_MATH_VERIFY_TLS_KEY_FILE", "server.key")
    listener_type = MagicMock()
    monkeypatch.setattr(server, "ThreadingHTTPServer", listener_type)

    with pytest.raises(ValueError, match="must be true or false"):
        server.main()

    listener_type.assert_not_called()


@pytest.mark.parametrize("opt_in", ["true", "false", 1, 0, None, []])
def test_listener_policy_requires_an_actual_boolean(opt_in) -> None:
    with pytest.raises(ValueError, match="must be a boolean"):
        server._validate_listener_policy("test", "127.0.0.1", opt_in, False)


@pytest.mark.parametrize("host", [2130706433, 0, None, b"127.0.0.1", ["127.0.0.1"]])
def test_listener_policy_rejects_non_string_hosts(host) -> None:
    with pytest.raises(ValueError, match="HOST must be a string"):
        server._validate_listener_policy("test", host, True, False)


@pytest.mark.parametrize("host", ["localhost", "math-verification-worker", "::1", "::ffff:127.0.0.1", "2130706433"])
def test_listener_policy_rejects_direct_non_literal_loopback_hosts(host) -> None:
    with pytest.raises(ValueError, match="literal IPv4 loopback"):
        server._validate_listener_policy("test", host, True, False)


@pytest.mark.parametrize("configured_name", ["EDUGRADE_MATH_VERIFY_TLS_CERT_FILE", "EDUGRADE_MATH_VERIFY_TLS_KEY_FILE"])
def test_partial_tls_configuration_is_rejected_before_bind(monkeypatch, configured_name) -> None:
    monkeypatch.setenv("EDUGRADE_ENV", "test")
    monkeypatch.setenv("EDUGRADE_MATH_VERIFY_HOST", "127.0.0.1")
    monkeypatch.setenv("EDUGRADE_MATH_VERIFY_ALLOW_INSECURE_LOOPBACK_HTTP", "true")
    monkeypatch.setenv(configured_name, "configured.pem")
    listener_type = MagicMock()
    monkeypatch.setattr(server, "ThreadingHTTPServer", listener_type)

    with pytest.raises(ValueError, match="configured together"):
        server.main()

    listener_type.assert_not_called()


def test_certificate_keeps_tls_enabled_with_development_cleartext_opt_in(monkeypatch) -> None:
    monkeypatch.setenv("EDUGRADE_ENV", "development")
    monkeypatch.setenv("EDUGRADE_MATH_VERIFY_ALLOW_INSECURE_LOOPBACK_HTTP", "true")
    monkeypatch.setenv("EDUGRADE_MATH_VERIFY_TLS_CERT_FILE", "server.crt")
    monkeypatch.setenv("EDUGRADE_MATH_VERIFY_TLS_KEY_FILE", "server.key")
    listener = MagicMock()
    listener_type = MagicMock(return_value=listener)
    context = MagicMock()
    monkeypatch.setattr(server, "ThreadingHTTPServer", listener_type)
    monkeypatch.setattr(server.ssl, "SSLContext", MagicMock(return_value=context))

    server.main()

    listener_type.assert_called_once_with(("0.0.0.0", 8092), server.Handler)
    context.load_cert_chain.assert_called_once_with("server.crt", "server.key")
    context.wrap_socket.assert_called_once()
    listener.serve_forever.assert_called_once_with()
    listener.server_close.assert_called_once_with()


def test_tls_load_and_wrap_finish_before_serving(monkeypatch) -> None:
    monkeypatch.setenv("EDUGRADE_MATH_VERIFY_TLS_CERT_FILE", "server.crt")
    monkeypatch.setenv("EDUGRADE_MATH_VERIFY_TLS_KEY_FILE", "server.key")
    listener = MagicMock()
    context = MagicMock()
    events = []
    context.load_cert_chain.side_effect = lambda *_args: events.append("load")
    context.wrap_socket.side_effect = lambda *_args, **_kwargs: events.append("wrap")
    listener.serve_forever.side_effect = lambda: events.append("serve")
    listener.server_close.side_effect = lambda: events.append("close")
    monkeypatch.setattr(server, "ThreadingHTTPServer", MagicMock(return_value=listener))
    monkeypatch.setattr(server.ssl, "SSLContext", MagicMock(return_value=context))

    server.main()

    assert events == ["load", "wrap", "serve", "close"]


def test_tls_wrap_failure_closes_listener_without_serving(monkeypatch) -> None:
    monkeypatch.setenv("EDUGRADE_MATH_VERIFY_TLS_CERT_FILE", "server.crt")
    monkeypatch.setenv("EDUGRADE_MATH_VERIFY_TLS_KEY_FILE", "server.key")
    listener = MagicMock()
    context = MagicMock()
    context.wrap_socket.side_effect = ssl.SSLError("wrap failed")
    monkeypatch.setattr(server, "ThreadingHTTPServer", MagicMock(return_value=listener))
    monkeypatch.setattr(server.ssl, "SSLContext", MagicMock(return_value=context))

    with pytest.raises(ssl.SSLError, match="wrap failed"):
        server.main()

    context.load_cert_chain.assert_called_once_with("server.crt", "server.key")
    listener.serve_forever.assert_not_called()
    listener.server_close.assert_called_once_with()


def test_invalid_tls_certificate_closes_listener(monkeypatch) -> None:
    monkeypatch.setenv("EDUGRADE_ENV", "production")
    monkeypatch.setenv("EDUGRADE_MATH_VERIFY_TLS_CERT_FILE", "server.crt")
    monkeypatch.setenv("EDUGRADE_MATH_VERIFY_TLS_KEY_FILE", "server.key")
    listener = MagicMock()
    context = MagicMock()
    context.load_cert_chain.side_effect = OSError("invalid certificate")
    monkeypatch.setattr(server, "ThreadingHTTPServer", MagicMock(return_value=listener))
    monkeypatch.setattr(server.ssl, "SSLContext", MagicMock(return_value=context))

    with pytest.raises(OSError, match="invalid certificate"):
        server.main()

    listener.serve_forever.assert_not_called()
    listener.server_close.assert_called_once_with()
