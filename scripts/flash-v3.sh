#!/usr/bin/env bash
set -euo pipefail

expected_sha256='af457547495d40ad1e42647a7d76a7f0bd2b757f3b65a1f082876821c5b3d75c'
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_dir=$(cd -- "$script_dir/.." && pwd)
flasher="$repo_dir/build/eeprom-flash"

if (( $# != 4 )); then
    printf 'usage: sudo %s /dev/hidrawN PATCHED.rom FULL_BACKUP.bin POST_FLASH_READ.bin\n' "$0" >&2
    exit 2
fi

if (( EUID != 0 )); then
    printf 'run as root so hidraw access remains consistent for the entire flash\n' >&2
    exit 1
fi

if [[ ! -x "$flasher" ]]; then
    printf 'missing %s; run make first\n' "$flasher" >&2
    exit 1
fi

actual_sha256=$(sha256sum -- "$2" | awk '{print $1}')
if [[ "$actual_sha256" != "$expected_sha256" ]]; then
    printf 'refusing ROM: expected v3 SHA-256 %s, got %s\n' "$expected_sha256" "$actual_sha256" >&2
    exit 1
fi

exec "$flasher" "$1" "$2" "$3" "$4"
