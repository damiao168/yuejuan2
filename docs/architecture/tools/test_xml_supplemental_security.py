"""补充 SVG 预览和字号核对的解析回归；所有输出都限制在临时目录。"""

import hashlib
import json

import build_preview
import pytest
import verify_typography
from defusedxml.common import DTDForbidden

DECLARATIONS = [
    '<!DOCTYPE {root}>',
    '<!DOCTYPE {root} [<!ENTITY text "expanded">]>',
    '<!DOCTYPE {root} [<!ENTITY text SYSTEM "file:///synthetic-secret">]>',
    '<!DOCTYPE {root} SYSTEM "http://127.0.0.1:9/synthetic.dtd">',
    '<!DOCTYPE {root} [<!ENTITY a "x"><!ENTITY text "&a;&a;&a;">]>',
]
SVG = (
    '<svg xmlns="http://www.w3.org/2000/svg" width="1200px" height="800px">'
    '<text style="font-family: \'Microsoft YaHei\';">合成图纸</text></svg>'
)
DRAWIO = (
    '<mxfile><diagram id="page-1"><mxGraphModel><root>'
    '<mxCell id="node-1" details="sample" evidence="README.md" vertex="1" '
    'implementationStatus="implemented"/></root></mxGraphModel></diagram></mxfile>'
)


def _write_json(path, value):
    path.write_text(json.dumps(value, ensure_ascii=False), encoding="utf-8")


def _prepare_preview(tmp_path, svg):
    (tmp_path / "evidence").mkdir()
    (tmp_path / "previews").mkdir()
    _write_json(tmp_path / "evidence/native-render-validation.json", {"results": [
        {"file": "01-fixture.drawio", "index": 1, "title": "合成预览", "preview": "previews/fixture.svg"},
    ]})
    (tmp_path / "previews/fixture.svg").write_text(svg, encoding="utf-8")
    output = tmp_path / "previews/index.html"
    output.write_bytes(b"previous-preview")
    return output


def _prepare_typography(tmp_path, raw):
    (tmp_path / "evidence").mkdir()
    (tmp_path / "diagrams").mkdir()
    (tmp_path / "specs").mkdir()
    (tmp_path / "diagrams/fixture.drawio").write_bytes(raw)
    _write_json(tmp_path / "evidence/typography-before.json", [{
        "file": "fixture.drawio",
        "pages": [{"id": "page-1", "cells": [{
            "id": "node-1", "details": "sample", "evidence": "README.md",
            "source": None, "target": None, "vertex": "1", "edge": None,
            "implementationStatus": "implemented",
        }]}],
    }])
    _write_json(tmp_path / "evidence/native-render-validation.json", {
        "inputs": [{"file": "fixture.drawio", "source_sha256": hashlib.sha256(raw).hexdigest()}],
        "results": [{"file": "fixture.drawio", "id": "page-1", "dangling": 0, "overflow": [], "obstructions": []}],
    })
    _write_json(tmp_path / "evidence/diagram-validation.json", {"errors": []})
    _write_json(tmp_path / "specs/fixture.json", {"pages": [{"nodes": [{"title_font_size": 40}]}]})
    output = tmp_path / "evidence/typography-validation.json"
    output.write_bytes(b"previous-validation")
    return output


def test_preview_keeps_svg_dimensions_and_changes_only_embedded_font_fallback(tmp_path, monkeypatch):
    output = _prepare_preview(tmp_path, SVG)
    monkeypatch.setattr(build_preview, "BASE", tmp_path)

    build_preview.main()

    preview = output.read_text(encoding="utf-8")
    assert 'data-width="1200" data-height="800"' in preview
    assert "font-family: 'Microsoft YaHei',sans-serif;" in preview
    assert "合成预览" in preview
    assert (tmp_path / "previews/fixture.svg").read_text(encoding="utf-8") == SVG


@pytest.mark.parametrize("declaration", DECLARATIONS)
def test_preview_rejects_dtd_before_overwriting_previous_output(tmp_path, monkeypatch, declaration):
    # 即使预览报告已存在，输入解析失败也不能覆盖此前离线预览。
    output = _prepare_preview(tmp_path, declaration.format(root="svg") + SVG)
    monkeypatch.setattr(build_preview, "BASE", tmp_path)

    with pytest.raises(DTDForbidden):
        build_preview.main()

    assert output.read_bytes() == b"previous-preview"


@pytest.mark.parametrize("dimension", ["0px", "-1px"])
def test_preview_still_rejects_nonpositive_dimensions(tmp_path, monkeypatch, dimension):
    output = _prepare_preview(tmp_path, SVG.replace("1200px", dimension))
    monkeypatch.setattr(build_preview, "BASE", tmp_path)

    with pytest.raises(ValueError, match="无效的原生SVG尺寸"):
        build_preview.main()

    assert output.read_bytes() == b"previous-preview"


def test_typography_keeps_content_hash_and_display_validation(tmp_path, monkeypatch):
    raw = DRAWIO.encode("utf-8")
    output = _prepare_typography(tmp_path, raw)
    monkeypatch.setattr(verify_typography, "BASE", tmp_path)

    assert verify_typography.main() is False

    report = json.loads(output.read_text(encoding="utf-8"))
    assert report["passed"] is True
    assert report["content_and_relations_preserved"] is True
    assert report["errors"] == []
    assert report["pages"] == 1
    assert report["files"] == [{"file": "fixture.drawio", "sha256": hashlib.sha256(raw).hexdigest(), "pages": 1}]
    assert report["font_sizes"]["node_title_range"] == [40, 40]
    assert (tmp_path / "diagrams/fixture.drawio").read_bytes() == raw


@pytest.mark.parametrize("declaration", DECLARATIONS)
@pytest.mark.parametrize("encoding", ["utf-8", "utf-16"])
def test_typography_rejects_dtd_before_overwriting_previous_report(tmp_path, monkeypatch, declaration, encoding):
    raw = (f'<?xml version="1.0" encoding="{encoding}"?>' + declaration.format(root="mxfile") + DRAWIO).encode(encoding)
    output = _prepare_typography(tmp_path, raw)
    monkeypatch.setattr(verify_typography, "BASE", tmp_path)

    with pytest.raises(DTDForbidden):
        verify_typography.main()

    assert output.read_bytes() == b"previous-validation"
    assert (tmp_path / "diagrams/fixture.drawio").read_bytes() == raw
