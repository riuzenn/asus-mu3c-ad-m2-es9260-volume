# v3 验证记录

首次验证日期：2026-09-19 CST。

## 离线产物

- Base：官方 ES9260 `3.6.14` external ROM；
- Base SHA-256：`fc162ef47d0b62ede04221d4d0b2f3486935f7a1a609006a96b3e560d63def88`；
- v3 SHA-256：`af457547495d40ad1e42647a7d76a7f0bd2b757f3b65a1f082876821c5b3d75c`；
- 长度：15,400 bytes；
- header checksum：`0x6fef`，存储值与重算值一致。

补丁器生成的 v3 与实机刷入使用的候选文件逐字节相同。8051 code/pre-descriptor data 与 configuration 后的运行时数据保持不变。

## EEPROM 写入与冷启动

在目标设备上，刷写过程通过了：

1. erase 前实时完整 32 KiB 与提供备份逐字节比较；
2. header byte 0 保持 `0xff` 时的 staged 15,400-byte ROM 读回；
3. 设备配置尾部恢复后的完整 32 KiB pre-commit 比较；
4. 提交 header 后的最终完整 32 KiB 比较；
5. 物理断电重插后的独立完整 EEPROM 读回。

公开记录不包含设备专属 EEPROM dump 或其哈希。

## USB 枚举

物理重插后，设备以 Full Speed 枚举，USB Audio playback/capture 与 HID interface 均成功绑定，未观察到 descriptor、audio 或 HID 错误。

- Configuration `wTotalLength`：`0x015f`；
- AudioControl `wTotalLength`：`0x006a`；
- interface 数量：4；
- 播放：16/24-bit，48/96 kHz；
- 录音：16/24-bit，24/48 kHz，mono；
- Mixer8 sources：Entity `7,2`。

## UAC 控件

Linux ALSA 同时枚举并完成 1-step 可逆 SET/GET：

| 控件 | Entity | 验证 |
|---|---:|---|
| PCM Playback | FU2 | 左右声道分别写入、读回并恢复 |
| Headset Capture | FU5 | master 增益写入、读回并恢复 |
| Headset Playback | FU7 | sidetone master 写入、读回并恢复 |

这证明三个控制请求路径在 Linux/USB 层可用，不等同于所有 host OS 都已经完成真实音频路径验证。

## Apple 端边界

较早的 v2 已在 iPhone 上确认系统播放滑块实际改变输出音量。v3 使用相同的 FU2 播放链路，同时通过扩展 descriptor 恢复 FU5 麦克风控制。首次发布前尚未在 iPhone/macOS 上重新完成 v3 的真实播放与麦克风输入增益测试。

因此当前结论是：v3 的 ROM、EEPROM、冷启动枚举和 Linux UAC 控制已验证；Apple v3 端到端行为仍是待验证项。
