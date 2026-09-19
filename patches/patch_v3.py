#!/usr/bin/env python3
"""Expose playback FU2 while retaining microphone FU5 and sidetone FU7.

The active four-interface configuration gains a ten-byte Feature Unit 2.
To keep the ROM and all post-descriptor data at their original offsets, the
following inactive three-interface profile is compacted by ten bytes: its two
four-rate playback format descriptors retain 48/96 kHz, and a two-byte vendor
descriptor keeps the profile's descriptor stream structurally bounded.

Only the byte-for-byte known ASUS ES9260 3.6.14 external ROM is accepted.
"""

from __future__ import annotations

import argparse
import hashlib
import sys
from pathlib import Path


BASE_SHA256 = "fc162ef47d0b62ede04221d4d0b2f3486935f7a1a609006a96b3e560d63def88"
ROM_BYTES = 15400

CONFIG1_SIGNATURE = bytes.fromhex(
    "09025501040100a000090400000001010000"
)
CONFIG2_SIGNATURE = bytes.fromhex(
    "0902ba00030100a000090400000001010000"
)
PLAYBACK_FU2 = bytes.fromhex("0a240602010101020200")
MIXER8_OLD = bytes.fromhex("0d240408020701020300000000")
MIXER8_NEW = bytes.fromhex("0d240408020702020300000000")
CONFIG2_FORMATS = {
    bytes.fromhex("142402010202100480bb0000770144ac00885801"):
        bytes.fromhex("0e2402010202100280bb00007701"),
    bytes.fromhex("142402010203180480bb0000770144ac00885801"):
        bytes.fromhex("0e2402010203180280bb00007701"),
}
VENDOR_PADDING = bytes.fromhex("02ff")


REQUIRED_CODE_SIGNATURES = (
    (
        "Entity 2 GET_CUR left/right playback volume",
        bytes.fromhex(
            "90c005e0ffbf023690c002e0ffbf011190e073e090c040f0"
            "90e074e0ff90c041f090c002e0ffbf021190e075e090c040f0"
            "90e076e0ff90c041f0"
        ),
    ),
    (
        "Entity 2 SET_CUR left/right dispatch",
        bytes.fromhex(
            "90c005e0ffbf022690c002e07004d226803e90c002e0ffbf0104"
            "d235803290c002e0ffbf0204d236"
        ),
    ),
    (
        "Entity 2 left playback hardware path",
        bytes.fromhex(
            "103502804b90c080e090e073f090c081e090e074f090e073e0fe"
            "a3e0ffc3e49ef582e49ff583121b10"
        ),
    ),
    (
        "Entity 2 right playback hardware path",
        bytes.fromhex(
            "103602803290c080e090e075f090c081e090e076f090e075e0fe"
            "a3e0ffc3e49ef582e49ff583121b42"
        ),
    ),
    (
        "Entity 2 playback mute hardware path",
        bytes.fromhex("102802801090c080e01392229242121bb475820122"),
    ),
    (
        "Entity 5 microphone volume hardware path",
        bytes.fromhex(
            "102a028018120e11af82787ca607c3e49ff582121bed121b08"
        ),
    ),
)


def digest(data: bytes | bytearray) -> str:
    return hashlib.sha256(data).hexdigest()


def checksum(data: bytes | bytearray) -> int:
    return sum(data[0x10:]) & 0xFFFF


def require_unique(data: bytes | bytearray, name: str, value: bytes) -> int:
    first = data.find(value)
    if first < 0:
        raise ValueError(f"signature not found: {name}")
    if data.find(value, first + 1) >= 0:
        raise ValueError(f"signature is not unique: {name}")
    return first


def parse_descriptors(blob: bytes) -> list[bytes]:
    descriptors: list[bytes] = []
    offset = 0
    while offset < len(blob):
        length = blob[offset]
        if length < 2 or offset + length > len(blob):
            raise ValueError(f"invalid descriptor at relative offset 0x{offset:x}")
        descriptors.append(blob[offset : offset + length])
        offset += length
    return descriptors


def compact_config2(config: bytes) -> bytes:
    descriptors = parse_descriptors(config)
    replacements = dict(CONFIG2_FORMATS)
    result: list[bytes] = []
    for descriptor in descriptors:
        replacement = replacements.pop(descriptor, None)
        result.append(replacement if replacement is not None else descriptor)
    if replacements:
        raise ValueError("inactive configuration format descriptors changed")
    result.append(VENDOR_PADDING)
    compacted = bytearray(b"".join(result))
    compacted[2:4] = len(compacted).to_bytes(2, "little")
    if len(compacted) != len(config) - len(PLAYBACK_FU2):
        raise AssertionError("inactive configuration did not release ten bytes")
    parse_descriptors(bytes(compacted))
    return bytes(compacted)


def expand_config1(config: bytes) -> bytes:
    mixer = require_unique(config, "Mixer Unit 8", MIXER8_OLD)
    expanded = bytearray(config[:mixer] + PLAYBACK_FU2 + config[mixer:])
    expanded[mixer + len(PLAYBACK_FU2) : mixer + len(PLAYBACK_FU2) + len(MIXER8_OLD)] = MIXER8_NEW
    expanded[2:4] = len(expanded).to_bytes(2, "little")

    header = require_unique(expanded, "AudioControl header", bytes.fromhex("0a240100016000020102"))
    expanded[header + 5 : header + 7] = (0x006A).to_bytes(2, "little")
    parse_descriptors(bytes(expanded))
    return bytes(expanded)


def audio_entities(config: bytes) -> dict[int, bytes]:
    entities: dict[int, bytes] = {}
    in_audio_control = False
    for descriptor in parse_descriptors(config):
        if descriptor[1] == 0x04:
            in_audio_control = (
                len(descriptor) >= 9
                and descriptor[2] == 0
                and descriptor[3] == 0
                and descriptor[5] == 1
                and descriptor[6] == 1
            )
        elif (
            in_audio_control
            and descriptor[1] == 0x24
            and descriptor[2] in (2, 3, 4, 5, 6, 7, 8)
        ):
            entity_id = descriptor[3]
            if entity_id in entities:
                raise ValueError(f"duplicate AudioControl Entity {entity_id}")
            entities[entity_id] = descriptor
    return entities


def verify_config1(config: bytes) -> None:
    if len(config) != 0x015F or int.from_bytes(config[2:4], "little") != len(config):
        raise ValueError("unexpected active configuration length")
    entities = audio_entities(config)
    expected = {
        2: PLAYBACK_FU2,
        5: bytes.fromhex("0a240605040103000000"),
        6: bytes.fromhex("092403060101000500"),
        7: bytes.fromhex("092406071001030000"),
        8: MIXER8_NEW,
    }
    for entity_id, descriptor in expected.items():
        if entities.get(entity_id) != descriptor:
            raise ValueError(f"unexpected active Entity {entity_id}")
    if entities.get(3) != bytes.fromhex("092403030204000800"):
        raise ValueError("headset Output Terminal no longer uses Mixer 8")

    ac_header = require_unique(config, "expanded AudioControl header", bytes.fromhex("0a240100016a00020102"))
    ac_end = ac_header + int.from_bytes(config[ac_header + 5 : ac_header + 7], "little")
    next_interface = config.find(bytes.fromhex("090401000001020000"), ac_header)
    if ac_end != next_interface:
        raise ValueError("AudioControl wTotalLength does not end at interface 1")


def verify_config2(config: bytes) -> None:
    if len(config) != 0x00B0 or int.from_bytes(config[2:4], "little") != len(config):
        raise ValueError("unexpected inactive configuration length")
    descriptors = parse_descriptors(config)
    if descriptors[-1] != VENDOR_PADDING:
        raise ValueError("inactive configuration padding is missing")
    formats = [
        descriptor
        for descriptor in descriptors
        if len(descriptor) == 14
        and descriptor[1] == 0x24
        and descriptor[2] == 0x02
        and descriptor[3] == 0x01
    ]
    expected = set(CONFIG2_FORMATS.values())
    if set(formats) != expected:
        raise ValueError("inactive configuration sample-rate compaction mismatch")


def patch_rom(source: bytes) -> tuple[bytes, int, int]:
    if len(source) != ROM_BYTES or digest(source) != BASE_SHA256:
        raise ValueError("use the unmodified ASUS ES9260 3.6.14 ROM")
    if source[:2] != b"E\x04" or int.from_bytes(source[2:4], "little") != checksum(source):
        raise ValueError("invalid ES9260 ROM header/checksum")
    for name, signature in REQUIRED_CODE_SIGNATURES:
        require_unique(source, name, signature)

    config1_start = require_unique(source, "active configuration", CONFIG1_SIGNATURE)
    config1_length = int.from_bytes(source[config1_start + 2 : config1_start + 4], "little")
    config2_start = require_unique(source, "inactive configuration", CONFIG2_SIGNATURE)
    if config1_start + config1_length != config2_start:
        raise ValueError("configuration layout changed")
    config2_length = int.from_bytes(source[config2_start + 2 : config2_start + 4], "little")
    config2_end = config2_start + config2_length

    config1 = expand_config1(source[config1_start:config2_start])
    config2 = compact_config2(source[config2_start:config2_end])
    image = bytearray(
        source[:config1_start] + config1 + config2 + source[config2_end:]
    )
    if len(image) != len(source):
        raise AssertionError("ROM length changed")
    image[2:4] = checksum(image).to_bytes(2, "little")

    new_config2_start = config1_start + len(config1)
    new_config2_end = new_config2_start + len(config2)
    verify_config1(bytes(image[config1_start:new_config2_start]))
    verify_config2(bytes(image[new_config2_start:new_config2_end]))
    if new_config2_end != config2_end:
        raise AssertionError("post-configuration data moved")
    if image[0x10:config1_start] != source[0x10:config1_start]:
        raise AssertionError("8051 code or pre-descriptor data changed")
    if image[config2_end:] != source[config2_end:]:
        raise AssertionError("post-descriptor runtime data changed")
    if int.from_bytes(image[2:4], "little") != checksum(image):
        raise AssertionError("patched checksum mismatch")
    return bytes(image), config1_start, new_config2_start


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("input", type=Path, help="unmodified ASUS 3.6.14 ROM")
    parser.add_argument("output", type=Path, help="v3 output ROM")
    args = parser.parse_args()
    try:
        source = args.input.read_bytes()
        patched, config1, config2 = patch_rom(source)
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_bytes(patched)
    except (OSError, ValueError, AssertionError) as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 1
    print(f"input SHA-256:  {digest(source)}")
    print(f"output SHA-256: {digest(patched)}")
    print(f"checksum:       0x{checksum(patched):04x}")
    print(f"active config:  0x{config1:04x}, 0x0155 -> 0x015f")
    print(f"inactive config moved: 0x{config2 - 10:04x} -> 0x{config2:04x}")
    print("8051 code and data after both configurations: unchanged")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
