"""Synthetic real OOXML packages exercise crop/frame geometry, not wording."""
from pathlib import Path
import json
import importlib.util
import struct
import subprocess
import sys
import tempfile
import unittest
import zipfile
import zlib


ROOT = Path(__file__).resolve().parents[1]
CHECKER = ROOT / 'skills/otoha-content-review/scripts/check_pptx_images.py'


def png(width, height):
    def chunk(kind, data):
        return struct.pack('>I', len(data)) + kind + data + struct.pack('>I', zlib.crc32(kind + data))
    return (b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', struct.pack('>IIBBBBB', width, height, 8, 2, 0, 0, 0)) +
            chunk(b'IDAT', zlib.compress((b'\0' + b'\x80\xb0\x90' * width) * height)) + chunk(b'IEND', b''))


def fixture(path, width=100, height=100, frame=(100, 100), crop='', image=None, group=False,
            extra_blip='', tile=False, fill_rect=''):
    picture = f'''<p:pic><p:nvPicPr><p:cNvPr id="2" name="example"/>
    <p:cNvPicPr><a:picLocks noChangeAspect="1"/></p:cNvPicPr></p:nvPicPr>
    <p:blipFill><a:blip r:embed="img1"/>{crop}{'<a:tile/>' if tile else '<a:stretch>' + fill_rect + '</a:stretch>'}</p:blipFill>
    <p:spPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="{frame[0]}" cy="{frame[1]}"/></a:xfrm></p:spPr></p:pic>'''
    if group:
        picture = '<p:grpSp>' + picture + '</p:grpSp>'
    with zipfile.ZipFile(path, 'w') as out:
        out.writestr('ppt/presentation.xml', '<p:presentation xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><p:sldIdLst><p:sldId id="256" r:id="s1"/></p:sldIdLst></p:presentation>')
        out.writestr('ppt/_rels/presentation.xml.rels', '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="s1" Target="slides/slide1.xml" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide"/></Relationships>')
        out.writestr('ppt/slides/slide1.xml', '<p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><p:cSld><p:spTree>' + picture + extra_blip + '</p:spTree></p:cSld></p:sld>')
        out.writestr('ppt/slides/_rels/slide1.xml.rels', '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="img1" Target="../media/example.png" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image"/></Relationships>')
        out.writestr('ppt/media/example.png', png(width, height) if image is None else image)


class ContentImageGeometryTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='otoha-image-check-')
        self.folder = Path(self.temp.name)
        self.pptx = self.folder / 'example.pptx'

    def tearDown(self):
        self.temp.cleanup()

    def check(self, policy=None):
        command = [sys.executable, str(CHECKER), str(self.pptx), '--json']
        if policy is not None:
            path = self.folder / 'policy.json'
            path.write_text(json.dumps(policy))
            command.extend(['--policy', str(path)])
        result = subprocess.run(command, capture_output=True, text=True, check=False)
        self.assertFalse(result.stderr, result.stderr)
        return result.returncode, json.loads(result.stdout)

    def test_locked_aspect_declaration_does_not_hide_real_stretch(self):
        fixture(self.pptx, width=710, height=724, frame=(368, 288))
        code, report = self.check()
        self.assertEqual(code, 1)
        pic = report['pictures'][0]
        self.assertEqual(pic['status'], 'blocked')
        self.assertAlmostEqual(pic['ratio_scale'], (368 / 288) / (710 / 724))
        self.assertGreater(pic['aspect_error'], 0.30)
        self.assertEqual(pic['shape_id'], '2')
        self.assertEqual(pic['slide'], 1)

    def test_correct_equal_scale_passes(self):
        fixture(self.pptx, width=160, height=90, frame=(3200, 1800))
        code, report = self.check()
        self.assertEqual(code, 0)
        self.assertEqual(report['status'], 'pass')
        self.assertEqual(report['pictures'][0]['ratio_scale'], 1)

    def test_valid_crop_uses_effective_source_instead_of_full_image(self):
        fixture(self.pptx, width=300, height=100, frame=(1000, 1000),
                crop='<a:srcRect l="33333" r="33333"/>')
        code, report = self.check()
        self.assertEqual(code, 0)
        self.assertAlmostEqual(report['pictures'][0]['cropped_source_px']['width'], 100.002)
        self.assertLess(report['pictures'][0]['aspect_error'], 0.001)

    def test_stretch_exception_is_explicit_and_scoped(self):
        fixture(self.pptx, frame=(200, 100))
        code, report = self.check({'allowed_stretch': [{'slide': 1, 'shape_id': '2', 'reason': 'Approved stretchable texture'}]})
        self.assertEqual(code, 0)
        self.assertEqual(report['pictures'][0]['status'], 'allowed_stretch')
        self.assertEqual(self.check({'allowed_stretch': [{'slide': 1, 'shape_id': '3', 'reason': 'Other object'}]})[0], 1)

    def test_missing_exception_reason_is_not_a_bypass(self):
        fixture(self.pptx, frame=(200, 100))
        self.assertEqual(self.check({'allowed_stretch': [{'slide': 1, 'shape_id': '2', 'reason': ''}]})[0], 2)

    def test_unknown_format_is_unverified_not_pass(self):
        fixture(self.pptx, image=b'<svg width="100" height="100"/>')
        code, report = self.check()
        self.assertEqual(code, 2)
        self.assertEqual(report['status'], 'unverified')

    def test_group_transform_and_tiled_fill_are_not_silently_passed(self):
        fixture(self.pptx, group=True)
        self.assertEqual(self.check()[0], 2)
        fixture(self.pptx, tile=True)
        self.assertEqual(self.check()[0], 2)

    def test_destination_fill_crop_is_not_treated_as_full_frame(self):
        fixture(self.pptx, fill_rect='<a:fillRect l="20000"/>')
        self.assertEqual(self.check()[0], 2)

    def test_jpeg_dimensions_skip_metadata_and_use_sof(self):
        spec = importlib.util.spec_from_file_location('image_check', CHECKER)
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        # A minimal header probe, not a claim that this is a rendered JPEG.
        header = (b'\xff\xd8\xff\xe0\x00\x06TEST' + b'\xff\xc0\x00\x11\x08' +
                  struct.pack('>HH', 90, 160) + b'\x03\x01\x11\x00\x02\x11\x00\x03\x11\x00')
        self.assertEqual(module.image_dimensions(header), (160, 90))

    def test_image_fill_outside_picture_is_reported(self):
        fixture(self.pptx, extra_blip='<p:sp><p:spPr><a:blipFill><a:blip r:embed="img1"/></a:blipFill></p:spPr></p:sp>')
        code, report = self.check()
        self.assertEqual(code, 2)
        self.assertEqual(report['pictures'][0]['status'], 'pass')
        self.assertTrue(report['issues'])

    def test_no_image_package_or_missing_file_does_not_pass(self):
        self.pptx.write_bytes(b'not a PPTX')
        self.assertEqual(self.check()[0], 2)
        self.pptx.unlink()
        self.assertEqual(self.check()[0], 2)


if __name__ == '__main__':
    unittest.main()
