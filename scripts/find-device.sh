#!/usr/bin/env bash
set -euo pipefail

found=0
for sys_node in /sys/class/hidraw/hidraw*; do
    [[ -e "$sys_node/device/uevent" ]] || continue
    if grep -Eq '^HID_ID=[^:]+:00000495:00003042$' "$sys_node/device/uevent"; then
        printf '/dev/%s\n' "${sys_node##*/}"
        found=1
    fi
done

if (( found == 0 )); then
    printf 'ASUS MU3C-AD(M2) HID interface 0495:3042 not found\n' >&2
    exit 1
fi
