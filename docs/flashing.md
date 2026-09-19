# Linux 刷写与恢复流程

本流程只适用于 USB `0495:3042`、官方 ES9260 `3.6.14` base。建议使用直接连接的 USB 端口，关闭会访问该 HID interface 的更新软件，并确保刷写期间不会休眠或断电。

## 1. 编译与确认设备

```bash
make
HID=$(scripts/find-device.sh)
printf '%s\n' "$HID"
```

如果脚本返回多个节点，不要直接刷写；先逐个用 `udevadm info` 确认拓扑。不要凭 `/dev/hidrawN` 的编号永久识别设备，因为重插后编号可能变化。

## 2. 生成并检查 v3 ROM

```bash
python3 patches/patch_v3.py \
  AppData/ES9260_3.6.14_EXT.rom \
  ES9260_3.6.14_EXT_AppleVolumeMic_v3.rom
sha256sum ES9260_3.6.14_EXT_AppleVolumeMic_v3.rom
```

结果必须是：

```text
af457547495d40ad1e42647a7d76a7f0bd2b757f3b65a1f082876821c5b3d75c
```

## 3. 连续读取两份完整备份

```bash
sudo build/eeprom-read "$HID" backup-1.bin
sudo build/eeprom-read "$HID" backup-2.bin
cmp backup-1.bin backup-2.bin
sha256sum backup-1.bin backup-2.bin
stat -c '%n %s bytes' backup-1.bin backup-2.bin
```

`cmp` 必须无输出，两个文件大小都必须为 32,768 bytes。妥善离线保存这两份设备专属备份，不要提交到公开仓库。

## 4. 刷入

```bash
sudo scripts/flash-v3.sh \
  "$HID" \
  ES9260_3.6.14_EXT_AppleVolumeMic_v3.rom \
  backup-1.bin \
  post-flash.bin
```

脚本首先检查固定的 v3 SHA-256。底层刷写器随后重新读取实时 32 KiB EEPROM；如果它与 `backup-1.bin` 有任意一个 byte 不同，会在 erase 前拒绝继续。

成功时最后一行包含：

```text
FLASH COMPLETE: final 32 KiB readback matches patched ROM plus preserved configuration tail
```

不要把下载完成、programmed 进度或单独的 ROM 区比较当成成功；只有最终完整 32 KiB compare 通过才算完成。

## 5. 冷启动与独立复核

刷写完成后物理拔下 DAC，等待数秒，再重新插入。软件 reload 不能替代这一冷启动步骤。

```bash
HID=$(scripts/find-device.sh)
sudo build/eeprom-read "$HID" post-replug.bin
cmp post-flash.bin post-replug.bin
cmp -n 15400 ES9260_3.6.14_EXT_AppleVolumeMic_v3.rom post-replug.bin
lsusb -d 0495:3042 -v
aplay -l
arecord -l
```

Linux 应公布三组控件：

- `PCM Playback Switch/Volume`：FU2，左右声道；
- `Headset Capture Switch/Volume`：FU5，master；
- `Headset Playback Switch/Volume`：FU7，sidetone master。

使用 `amixer -c <card-number> contents` 查看实际 card。写值测试必须记录原值、只移动一步、读回确认并恢复，避免意外的大音量输出。

## 回滚官方 3.6.14

回滚时传入官方 `ES9260_3.6.14_EXT.rom` 作为 ROM，同时传入**此刻实时读取并验证一致**的 32 KiB 备份。底层刷写器接受合法的 3.6.14 ROM，不限制为 v3；`scripts/flash-v3.sh` 则专门限制 v3，回滚时不要使用它。

```bash
HID=$(scripts/find-device.sh)
sudo build/eeprom-read "$HID" rollback-live-1.bin
sudo build/eeprom-read "$HID" rollback-live-2.bin
cmp rollback-live-1.bin rollback-live-2.bin
sudo build/eeprom-flash \
  "$HID" \
  AppData/ES9260_3.6.14_EXT.rom \
  rollback-live-1.bin \
  rollback-post-flash.bin
```

完成后同样必须物理拔插并独立读回。如果设备已经无法枚举为 `0495:3042`，这些 hidraw 工具将无法工作，需要使用外部 SPI 编程器和此前保存的完整备份恢复。
