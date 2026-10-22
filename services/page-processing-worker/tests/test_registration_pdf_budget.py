import io
import pypdfium2 as pdfium
from page_processing.registration import _decode_page


def _pdf(sizes):
    document = pdfium.PdfDocument.new()
    for width, height in sizes:
        page = document.new_page(width, height)
        page.close()
    output = io.BytesIO()
    document.save(output)
    document.close()
    return output.getvalue()


def test_large_point_scan_template_is_rendered_within_registration_budget():
    image = _decode_page(_pdf([(3304, 2338)]), "application/pdf", 300, 1)
    assert max(image.shape[:2]) <= 4096
    assert image.shape[0] * image.shape[1] <= 16_000_000
    assert abs(image.shape[1] / image.shape[0] - 3304 / 2338) < .002


def test_selected_pdf_page_does_not_decode_other_oversized_pages():
    image = _decode_page(_pdf([(3304, 2338), (600, 400)]), "application/pdf", 300, 2)
    assert image.shape[:2] == (1667, 2500)
