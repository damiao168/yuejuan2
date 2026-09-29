from __future__ import annotations

import io

import numpy as np
import pytest
from image_quality.engine import ImageQualityError, analyze_and_normalize
from PIL import Image, ImageDraw, ImageFilter


def test_clear_image_passes_and_outputs_rgb_png() -> None:
    image_bytes = make_clear_answer_like_image()

    result = analyze_and_normalize(image_bytes)

    assert result.quality_status == "passed"
    assert result.quality_report["normalized_color_mode"] == "RGB"
    assert result.quality_report["metrics"]["sharpness_score"] > 0.5
    assert result.normalized_png.startswith(b"\x89PNG")
    assert result.quality_report["geometry"]["skew_correction_applied"] is False
    with Image.open(io.BytesIO(result.normalized_png)) as normalized:
        assert normalized.mode == "RGB"


def test_blurry_image_triggers_low_sharpness_review() -> None:
    clear = Image.open(io.BytesIO(make_clear_answer_like_image()))
    blurry = clear.filter(ImageFilter.GaussianBlur(radius=7))
    output = io.BytesIO()
    blurry.save(output, format="JPEG", quality=90)

    result = analyze_and_normalize(output.getvalue())

    assert result.quality_status == "review"
    assert result.quality_report["metrics"]["sharpness_score"] < 0.5
    assert any(issue["code"] == "low_sharpness" for issue in result.quality_issues)


@pytest.mark.parametrize(
    ("orientation", "expected_rotation", "expected_size"),
    [
        (1, 0, (320, 520)),
        (3, 180, (320, 520)),
        (6, 90, (520, 320)),
        (8, 270, (520, 320)),
    ],
)
def test_exif_rotation_records_transform(
    orientation: int, expected_rotation: int, expected_size: tuple[int, int]
) -> None:
    image = Image.new("RGB", (320, 520), "white")
    draw = ImageDraw.Draw(image)
    draw.rectangle((20, 20, 300, 500), outline="black", width=5)
    draw.line((40, 120, 280, 120), fill="black", width=3)
    exif = Image.Exif()
    exif[274] = orientation
    output = io.BytesIO()
    image.save(output, format="JPEG", exif=exif)

    result = analyze_and_normalize(output.getvalue())

    assert result.normalization_transform["exif_rotation_degrees"] == expected_rotation
    assert result.normalization_transform["normalized_pixel_width"] == expected_size[0]
    assert result.normalization_transform["normalized_pixel_height"] == expected_size[1]
    assert_transformed_corners_inside_canvas(result.normalization_transform)


def test_unreadable_image_is_rejected() -> None:
    with pytest.raises(ImageQualityError, match="unreadable_image"):
        analyze_and_normalize(b"not-an-image")


def test_oversized_dimensions_are_rejected_before_decode(monkeypatch) -> None:
    class OversizedImage:
        width = 100_000
        height = 100_000

        def __enter__(self):
            return self

        def __exit__(self, _exc_type, _exc, _traceback):
            return None

    monkeypatch.setattr("image_quality.engine.Image.open", lambda _: OversizedImage())
    with pytest.raises(ImageQualityError, match="image_dimensions_out_of_range"):
        analyze_and_normalize(b"header")


def test_detects_and_corrects_safe_small_skew() -> None:
    with Image.open(io.BytesIO(make_clear_answer_like_image())) as source:
        rotated = source.rotate(
            4.0, resample=Image.Resampling.BICUBIC, fillcolor="white"
        )
        image_bytes = encode_image(rotated)

    result = analyze_and_normalize(image_bytes)

    geometry = result.quality_report["geometry"]
    assert abs(abs(geometry["detected_skew_angle"]) - 4.0) < 1.5
    assert geometry["skew_confidence"] > 0.45
    assert geometry["skew_correction_applied"] is True
    assert result.normalization_transform["skew_correction_applied"] is True
    assert result.normalization_transform["content_rotation_degrees"] == pytest.approx(
        geometry["detected_skew_angle"], abs=0.0001
    )
    residual = analyze_and_normalize(result.normalized_png).quality_report["geometry"][
        "detected_skew_angle"
    ]
    assert abs(residual) < abs(geometry["detected_skew_angle"]) * 0.4
    assert_transformed_corners_inside_canvas(result.normalization_transform)


def test_low_confidence_page_is_not_rotated() -> None:
    image = Image.new("RGB", (800, 1100), "white")
    ImageDraw.Draw(image).line((370, 530, 430, 534), fill="black", width=2)

    result = analyze_and_normalize(encode_image(image))

    geometry = result.quality_report["geometry"]
    assert geometry["skew_confidence"] < 0.45
    assert geometry["skew_correction_applied"] is False
    assert result.normalization_transform["content_rotation_degrees"] == 0


@pytest.mark.parametrize("rotation", [22.0, 40.0])
def test_large_angle_is_not_treated_as_small_deskew_and_requires_review(
    rotation: float,
) -> None:
    with Image.open(io.BytesIO(make_clear_answer_like_image())) as source:
        rotated = source.rotate(
            rotation,
            resample=Image.Resampling.BICUBIC,
            expand=True,
            fillcolor="white",
        )

    result = analyze_and_normalize(encode_image(rotated))

    geometry = result.quality_report["geometry"]
    assert abs(abs(geometry["detected_skew_angle"]) - rotation) < 2.0
    assert geometry["skew_confidence"] >= 0.45
    assert geometry["skew_correction_applied"] is False
    assert result.normalization_transform["content_rotation_degrees"] == 0
    assert result.quality_status in {"review", "failed"}
    assert any(issue["code"] == "residual_skew" for issue in result.quality_issues)


@pytest.mark.parametrize("rotation", [90.0, 270.0])
def test_quarter_turn_requires_review_without_automatic_rotation(
    rotation: float,
) -> None:
    with Image.open(io.BytesIO(make_clear_answer_like_image())) as source:
        rotated = source.rotate(rotation, expand=True, fillcolor="white")

    result = analyze_and_normalize(encode_image(rotated))

    geometry = result.quality_report["geometry"]
    assert geometry["coarse_orientation"] == "landscape"
    assert geometry["skew_correction_applied"] is False
    assert result.quality_status in {"review", "failed"}
    assert any(
        issue["code"] == "large_rotation_risk" for issue in result.quality_issues
    )


def test_complete_page_border_scores_higher_than_cropped_border() -> None:
    complete = analyze_and_normalize(make_clear_answer_like_image())
    with Image.open(io.BytesIO(make_clear_answer_like_image())) as source:
        cropped = source.copy()
    ImageDraw.Draw(cropped).rectangle(
        (730, 0, cropped.width, cropped.height), fill="white"
    )
    cropped_result = analyze_and_normalize(encode_image(cropped))

    complete_score = complete.quality_report["metrics"]["border_completeness_score"]
    cropped_score = cropped_result.quality_report["metrics"][
        "border_completeness_score"
    ]
    assert complete_score > cropped_score
    assert complete.quality_report["geometry"]["page_border_status"] == "complete"


def test_trapezoid_has_more_perspective_risk_than_rectangle() -> None:
    rectangle = analyze_and_normalize(
        make_page_on_background([(80, 60), (720, 60), (720, 1040), (80, 1040)])
    )
    trapezoid = analyze_and_normalize(
        make_page_on_background([(190, 60), (610, 60), (740, 1040), (60, 1040)])
    )

    rectangle_risk = rectangle.quality_report["metrics"]["perspective_risk_score"]
    trapezoid_risk = trapezoid.quality_report["metrics"]["perspective_risk_score"]
    assert trapezoid_risk > rectangle_risk
    assert trapezoid.quality_report["geometry"]["perspective_confidence"] > 0.4
    assert trapezoid.quality_status == "failed"
    assert any(
        issue["code"] == "perspective_risk" for issue in trapezoid.quality_issues
    )


def test_gradient_shadow_has_more_risk_than_uniform_page() -> None:
    clean_bytes = make_clear_answer_like_image()
    with Image.open(io.BytesIO(clean_bytes)) as source:
        pixels = np.asarray(source.convert("RGB"), dtype=np.float32)
    gradient = np.linspace(1.0, 0.42, pixels.shape[1], dtype=np.float32)[None, :, None]
    shadowed = Image.fromarray(np.clip(pixels * gradient, 0, 255).astype(np.uint8))

    clean = analyze_and_normalize(clean_bytes)
    shadow = analyze_and_normalize(encode_image(shadowed))

    assert (
        shadow.quality_report["metrics"]["shadow_risk_score"]
        > clean.quality_report["metrics"]["shadow_risk_score"]
    )


def test_report_exposes_seven_quality_dimensions_and_hard_gates() -> None:
    result = analyze_and_normalize(make_clear_answer_like_image())

    report = result.quality_report
    assert set(report["dimensions"]) == {
        "completeness",
        "effective_resolution",
        "sharpness",
        "geometry",
        "illumination_contrast",
        "occlusion_reflection",
        "noise_compression",
    }
    assert 0 <= report["quality_score"] <= 100
    assert report["decision"] in {
        "PASS",
        "PASS_WITH_ENHANCEMENT",
        "LOW_QUALITY",
        "REJECT",
    }
    assert report["hard_gates"]
    severe_blur_gate = next(
        gate for gate in report["hard_gates"] if gate["code"] == "severe_blur"
    )
    assert severe_blur_gate["threshold"] == 0.12
    assert (
        report["predictor"]["calibration_status"]
        == "awaiting_real_answer_sheet_samples"
    )


def test_local_focus_map_detects_partially_blurred_answer_area() -> None:
    with Image.open(io.BytesIO(make_clear_answer_like_image())) as source:
        mixed = source.copy()
    bottom = mixed.crop((0, mixed.height // 2, mixed.width, mixed.height)).filter(
        ImageFilter.GaussianBlur(radius=10)
    )
    mixed.paste(bottom, (0, mixed.height // 2))

    result = analyze_and_normalize(encode_image(mixed))
    focus = result.quality_report["focus"]

    assert focus["ink_patch_count"] >= 8
    assert focus["focus_p20"] < focus["focus_median"]
    assert focus["bad_patch_ratio"] > 0
    assert len(focus["map"]) == focus["ink_patch_count"]
    assert any(item["status"] == "bad" for item in focus["map"])


def test_effective_resolution_is_a_hard_gate() -> None:
    image = Image.new("RGB", (900, 1250), "white")
    draw = ImageDraw.Draw(image)
    draw.rectangle((25, 25, 875, 1225), outline="black", width=4)
    for y in range(100, 1150, 70):
        draw.line((70, y, 830, y), fill="black", width=3)

    result = analyze_and_normalize(encode_image(image))

    assert result.quality_status == "failed"
    assert result.quality_report["decision"] == "REJECT"
    assert result.quality_report["metrics"]["effective_short_edge_px"] == 850
    assert any(
        issue["code"] == "low_effective_resolution" and issue["severity"] == "failed"
        for issue in result.quality_issues
    )


def test_quality_score_cannot_override_a_failing_hard_gate() -> None:
    image = Image.new("RGB", (900, 1250), "white")
    draw = ImageDraw.Draw(image)
    for y in range(70, 1190, 45):
        draw.line((40, y, 860, y), fill="black", width=3)

    result = analyze_and_normalize(encode_image(image))

    assert result.quality_report["quality_score"] > 0
    assert result.quality_report["decision"] == "REJECT"
    assert any(
        gate["status"] == "failed" for gate in result.quality_report["hard_gates"]
    )


def make_clear_answer_like_image() -> bytes:
    image = Image.new("RGB", (1800, 2475), "white")
    draw = ImageDraw.Draw(image)
    draw.rectangle((70, 70, 1730, 2405), outline="black", width=12)
    for y in range(240, 2200, 160):
        draw.line((180, y, 1620, y), fill="black", width=8)
        draw.line((180, y + 56, 960, y + 56), fill="black", width=6)
    for x in range(280, 1600, 240):
        draw.rectangle((x, 120, x + 48, 168), outline="black", width=6)
    output = io.BytesIO()
    image.save(output, format="JPEG", quality=95)
    return output.getvalue()


def make_page_on_background(points: list[tuple[int, int]]) -> bytes:
    scale = 2
    image = Image.new("RGB", (1600, 2200), (45, 45, 45))
    draw = ImageDraw.Draw(image)
    draw.polygon(
        [(x * scale, y * scale) for x, y in points],
        fill="white",
        outline="black",
        width=10,
    )
    return encode_image(image)


def encode_image(image: Image.Image) -> bytes:
    output = io.BytesIO()
    image.save(output, format="PNG")
    return output.getvalue()


# 逐角验证实际齐次变换结果，避免只检查输出宽高而漏掉旋转裁切。
def assert_transformed_corners_inside_canvas(transform: dict) -> None:
    width = transform["source_pixel_width"]
    height = transform["source_pixel_height"]
    matrix = np.asarray(transform["source_to_normalized_matrix"], dtype=np.float64)
    corners = np.asarray(
        [[0, 0, 1], [width - 1, 0, 1], [width - 1, height - 1, 1], [0, height - 1, 1]],
        dtype=np.float64,
    )
    mapped = (matrix @ corners.T).T
    mapped = mapped[:, :2] / mapped[:, 2:3]
    epsilon = 1e-5
    assert np.isfinite(mapped).all()
    assert np.all(mapped[:, 0] >= -epsilon)
    assert np.all(mapped[:, 1] >= -epsilon)
    assert np.all(mapped[:, 0] <= transform["normalized_pixel_width"] - 1 + epsilon)
    assert np.all(mapped[:, 1] <= transform["normalized_pixel_height"] - 1 + epsilon)
