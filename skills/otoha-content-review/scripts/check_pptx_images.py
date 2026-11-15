#!/usr/bin/env python3
"""Check actual crop/frame aspect ratios in PPTX slide pictures (stdlib only).

Exit 0: geometry passes (or no pictures); 1: aspect distortion; 2: unverified.
This does not judge visual quality, subject cropping, or native playback.
"""
import argparse
import hashlib
import io
import json
from pathlib import Path
import posixpath
import struct
import sys
import xml.etree.ElementTree as ET
import zipfile


NS = {
    'a': 'http://schemas.openxmlformats.org/drawingml/2006/main',
    'p': 'http://schemas.openxmlformats.org/presentationml/2006/main',
    'r': 'http://schemas.openxmlformats.org/officeDocument/2006/relationships',
    'rel': 'http://schemas.openxmlformats.org/package/2006/relationships',
}
RULE = 'otoha/content-image-aspect'
# 0.1% allows OOXML crop quantization, not visible 28-34% stretching.
ASPECT_TOLERANCE = 0.001


class Unverified(ValueError):
    pass


def image_dimensions(data):
    """Read intrinsic PNG/JPEG pixel dimensions without installing a decoder."""
    if data.startswith(b'\x89PNG\r\n\x1a\n') and data[12:16] == b'IHDR' and len(data) >= 24:
        width, height = struct.unpack('>II', data[16:24])
        if width and height:
            return width, height
    if data.startswith(b'\xff\xd8'):
        i = 2
        while i < len(data):
            if data[i] != 0xff:
                break
            while i < len(data) and data[i] == 0xff:
                i += 1
            if i >= len(data):
                break
            marker = data[i]
            i += 1
            if marker in {0x01, 0xd8, *range(0xd0, 0xd8)}:
                continue
            if marker in {0xd9, 0xda} or i + 2 > len(data):
                break
            length = struct.unpack('>H', data[i:i + 2])[0]
            if length < 2 or i + length > len(data):
                break
            if marker in {0xc0, 0xc1, 0xc2, 0xc3, 0xc5, 0xc6, 0xc7,
                          0xc9, 0xca, 0xcb, 0xcd, 0xce, 0xcf} and length >= 7:
                height, width = struct.unpack('>HH', data[i + 3:i + 7])
                if width and height:
                    return width, height
            i += length
    raise Unverified('unsupported or unreadable image dimensions; PNG/JPEG required')


def relationships(archive, part):
    folder, name = posixpath.split(part)
    rel_part = posixpath.join(folder, '_rels', name + '.rels')
    try:
        root = ET.fromstring(archive.read(rel_part))
    except KeyError:
        return {}
    result = {}
    for rel in root.findall('rel:Relationship', NS):
        target = rel.get('Target', '')
        result[rel.get('Id')] = {
            'part': (target.lstrip('/') if target.startswith('/') else
                     posixpath.normpath(posixpath.join(folder, target))),
            'external': rel.get('TargetMode') == 'External',
            'type': rel.get('Type', ''),
        }
    return result


def load_policy(path):
    if path is None:
        return {}
    value = json.loads(Path(path).read_text(encoding='utf-8'))
    if not isinstance(value, dict) or set(value) - {'allowed_stretch'}:
        raise ValueError('policy supports only allowed_stretch')
    entries = value.get('allowed_stretch', [])
    if not isinstance(entries, list):
        raise ValueError('allowed_stretch must be a list')
    allowed = {}
    for entry in entries:
        if not isinstance(entry, dict) or set(entry) != {'slide', 'shape_id', 'reason'}:
            raise ValueError('each exception requires slide, shape_id and reason')
        if (type(entry['slide']) is not int or entry['slide'] < 1 or
                not isinstance(entry['shape_id'], (str, int)) or
                not isinstance(entry['reason'], str) or not entry['reason'].strip()):
            raise ValueError('exception needs a positive slide, shape id, and nonempty reason')
        key = (entry['slide'], str(entry['shape_id']))
        if key in allowed:
            raise ValueError('duplicate stretch exception')
        allowed[key] = entry['reason'].strip()
    return allowed


def crop_fraction(value):
    return float(value[:-1]) / 100 if value.endswith('%') else float(value) / 100000


def check_picture(archive, slide, part, pic, rels, grouped, allowed):
    identity = pic.find('p:nvPicPr/p:cNvPr', NS)
    record = {'slide': slide, 'slide_part': part,
              'shape_id': identity.get('id') if identity is not None else None,
              'shape_name': identity.get('name', '') if identity is not None else ''}
    try:
        if record['shape_id'] is None:
            raise Unverified('picture has no stable shape id')
        if grouped:
            raise Unverified('group transforms are outside this checker scope')
        fill = pic.find('p:blipFill', NS)
        blips = [] if fill is None else fill.findall('a:blip', NS)
        if len(blips) != 1:
            raise Unverified('expected one picture blip')
        rid = blips[0].get('{' + NS['r'] + '}embed')
        rel = rels.get(rid)
        if rel is None or rel['external']:
            raise Unverified('image must resolve to an embedded package part')
        record['image_part'] = rel['part']
        width, height = image_dimensions(archive.read(rel['part']))
        record['source_px'] = {'width': width, 'height': height}
        if fill.find('a:tile', NS) is not None or fill.find('a:stretch', NS) is None:
            raise Unverified('only stretch-to-frame picture fills are supported')
        fill_rect = fill.find('a:stretch/a:fillRect', NS)
        if fill_rect is not None and any(crop_fraction(v) != 0 for v in fill_rect.attrib.values()):
            raise Unverified('nonzero destination fillRect needs separate geometry handling')
        crop = fill.find('a:srcRect', NS)
        fractions = {side: crop_fraction(crop.get(side, '0')) if crop is not None else 0.0
                     for side in ('l', 't', 'r', 'b')}
        if any(not 0 <= v < 1 for v in fractions.values()):
            raise Unverified('negative or out-of-image crop is outside this checker scope')
        source_w = width * (1 - fractions['l'] - fractions['r'])
        source_h = height * (1 - fractions['t'] - fractions['b'])
        if source_w <= 0 or source_h <= 0:
            raise Unverified('crop has no positive source area')
        record['crop'] = fractions
        record['cropped_source_px'] = {'width': source_w, 'height': source_h}
        extent = pic.find('p:spPr/a:xfrm/a:ext', NS)
        if extent is None:
            raise Unverified('picture frame extent is missing')
        frame_w, frame_h = int(extent.get('cx', '0')), int(extent.get('cy', '0'))
        if frame_w <= 0 or frame_h <= 0:
            raise Unverified('picture frame has no positive area')
        record['frame_emu'] = {'width': frame_w, 'height': frame_h}
        ratio_scale = (frame_w / frame_h) / (source_w / source_h)
        error = max(ratio_scale, 1 / ratio_scale) - 1
        record.update(ratio_scale=ratio_scale, aspect_error=error)
        reason = allowed.get((slide, record['shape_id']))
        if reason is not None:
            record.update(status='allowed_stretch', exception_reason=reason)
        elif error > ASPECT_TOLERANCE:
            record.update(status='blocked', reason='actual crop/frame aspect ratio differs')
        else:
            record['status'] = 'pass'
    except (Unverified, KeyError, ValueError, struct.error) as exc:
        record.update(status='unverified', reason=str(exc))
    return record


def check_pptx(path, allowed=None):
    path = Path(path)
    allowed = allowed or {}
    artifact_bytes = path.read_bytes()
    report = {'rule_id': RULE, 'artifact': str(path),
              'sha256': hashlib.sha256(artifact_bytes).hexdigest(),
              'scope': 'embedded PNG/JPEG slide pictures; geometry only, not visual acceptance',
              'relative_aspect_tolerance': ASPECT_TOLERANCE,
              'pictures': [], 'issues': []}
    with zipfile.ZipFile(io.BytesIO(artifact_bytes)) as archive:
        presentation = ET.fromstring(archive.read('ppt/presentation.xml'))
        entries = presentation.findall('p:sldIdLst/p:sldId', NS)
        if not entries:
            raise Unverified('no supported presentation slide order')
        deck_rels = relationships(archive, 'ppt/presentation.xml')
        inherited_seen = set()
        for slide, entry in enumerate(entries, 1):
            rel = deck_rels.get(entry.get('{' + NS['r'] + '}id'))
            if not rel or rel['external']:
                raise Unverified('slide relationship is missing or external')
            part = rel['part']
            root = ET.fromstring(archive.read(part))
            rels = relationships(archive, part)
            seen_blips = set()

            def walk(node, grouped=False):
                grouped = grouped or node.tag == '{' + NS['p'] + '}grpSp'
                if node.tag == '{' + NS['p'] + '}pic':
                    seen_blips.update(id(x) for x in node.findall('.//a:blip', NS))
                    report['pictures'].append(check_picture(archive, slide, part, node, rels, grouped, allowed))
                for child in node:
                    walk(child, grouped)

            walk(root)
            unmatched = sum(id(x) not in seen_blips for x in root.findall('.//a:blip', NS))
            if unmatched:
                report['issues'].append({'status': 'unverified', 'slide': slide, 'slide_part': part,
                                         'reason': f'{unmatched} image fill(s) outside picture containers'})
            # Images inherited from layouts/masters are not silently counted as checked.
            related = [v for v in rels.values() if v['type'].endswith('/slideLayout')]
            for layout in related:
                inherited = [layout] + [v for v in relationships(archive, layout['part']).values()
                                        if v['type'].endswith('/slideMaster')]
                for ancestor in inherited:
                    if ancestor['part'] in inherited_seen:
                        continue
                    inherited_seen.add(ancestor['part'])
                    tree = ET.fromstring(archive.read(ancestor['part']))
                    if tree.findall('.//a:blip', NS):
                        report['issues'].append({'status': 'unverified', 'slide': slide,
                                                 'slide_part': ancestor['part'],
                                                 'reason': 'inherited image geometry is outside this checker scope'})
        found = {(x['slide'], x['shape_id']) for x in report['pictures']}
        for key in allowed.keys() - found:
            report['issues'].append({'status': 'unverified', 'slide': key[0], 'shape_id': key[1],
                                     'reason': 'stretch exception did not match a picture'})
    report['issues'].extend(x for x in report['pictures'] if x['status'] in {'blocked', 'unverified'})
    states = {x['status'] for x in report['issues']}
    report['status'] = ('blocked' if 'blocked' in states else 'unverified' if states else
                        'pass' if report['pictures'] else 'not_applicable')
    report['exit_code'] = 1 if 'blocked' in states else 2 if states else 0
    return report


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('pptx', type=Path)
    parser.add_argument('--policy', type=Path,
                        help='JSON {"allowed_stretch":[{"slide":1,"shape_id":"2","reason":"..."}]}')
    parser.add_argument('--json', action='store_true', help='emit full structured diagnostics')
    args = parser.parse_args(argv)
    try:
        report = check_pptx(args.pptx, load_policy(args.policy))
    except (OSError, ValueError, KeyError, ET.ParseError, zipfile.BadZipFile) as exc:
        report = {'rule_id': RULE, 'artifact': str(args.pptx), 'status': 'unverified',
                  'issues': [{'status': 'unverified', 'reason': str(exc)}], 'exit_code': 2}
    if args.json:
        print(json.dumps(report, ensure_ascii=False, indent=2))
    else:
        print(f"{RULE}: {report['status']} ({len(report.get('pictures', []))} picture(s))")
        for issue in report['issues']:
            print(f"  slide {issue.get('slide', '?')} shape {issue.get('shape_id', '?')}: "
                  f"{issue['status']}: {issue.get('reason', '')}")
            if 'ratio_scale' in issue:
                print(f"    source crop={issue['cropped_source_px']} frame={issue['frame_emu']} "
                      f"horizontal/vertical scale={issue['ratio_scale']:.6f}")
        print('Geometry evidence only; visual quality and native rendering remain separate checks.')
    return report['exit_code']


if __name__ == '__main__':
    sys.exit(main())
