# 逆向分析与 v3 设计

## 问题根因

官方 ES9260 `3.6.14` 固件的活动 USB Audio configuration 中，播放数据由 USB Input Terminal 1 直接进入 Mixer Unit 8，再到 Headset Output Terminal 3。没有在播放拓扑里公布 Feature Unit，所以 macOS/iOS 无法把系统输出滑块绑定到 DAC 的硬件音量控制。

8051 程序中实际上保留了 Entity 2 的 UAC Feature Unit 请求处理路径：

- `GET_CUR` 左声道读取内部状态 `0xe073/0xe074`，右声道读取 `0xe075/0xe076`；
- `SET_CUR` 对 channel 1/2 分别置内部 dispatch bit 35/36；
- 左右声道硬件更新路径分别调用 code address `0x1b10` / `0x1b42`；
- mute 更新路径调用 `0x1bb4`，并关联 `0x8052` 的控制位；
- Entity 5 的麦克风增益硬件路径也独立存在。

因此问题不是缺少增益实现，而是活动 AudioControl descriptor 没有把已有的 Entity 2 插入信号链。

## 从 v1、v2 到 v3

v1 把播放端系统 UI 错接到 Entity 7。Entity 7 是 sidetone/耳返控制，SET/GET 能往返不代表它改变主播放输出。

v2 用 Feature Unit 2 替换了原来的 FU5 descriptor，iPhone 上已确认播放滑块与真实输出音量关联，但这样会让麦克风控制从活动拓扑中消失。

v3 不再复用 FU5 的空间，而是扩展 AudioControl descriptor 区，新增完整的 10-byte FU2，同时保留 FU5 和 FU7。

## v3 descriptor 拓扑

活动 configuration 位于 ROM file offset `0x37f8`，原长度 `0x0155`。v3 在 IT1 与 Mixer8 之间插入：

```text
0a 24 06 02 01 01 01 02 02 00
```

这是 Entity 2、source Entity 1、`bControlSize=1` 的 Feature Unit。其控制位公布 master mute，以及左右声道 volume。Mixer8 的 source 列表由 `7,1` 改为 `7,2`。

最终活动拓扑为：

```text
IT4  -> FU5 -> OT6                 microphone capture
IT16 -> FU7 --+                    sidetone
IT1  -> FU2 --+-> Mixer8 -> OT3    playback
```

对应长度变化：

| 范围 | 官方 3.6.14 | v3 |
|---|---:|---:|
| Active configuration `wTotalLength` | `0x0155` | `0x015f` |
| AudioControl `wTotalLength` | `0x0060` | `0x006a` |
| Mixer8 sources | `7,1` | `7,2` |

活动 configuration 原有的播放 16/24-bit、48/96 kHz，录音 16/24-bit、24/48 kHz mono，以及 HID interface 都未删减。

## 固定 ROM 长度与后部数据位置

ROM 总长度必须保持 15,400 bytes，并避免移动 configuration 之后可能由 8051 代码按固定地址访问的运行时表。活动 configuration 增加的 10 bytes 从紧随其后的备用 3-interface profile 回收：

- 备用 profile 从 `0x394d` 移到 `0x3957`；
- 其中两个播放 format descriptor 各自从 48/96/44.1/88.2 kHz 四个离散采样率缩减为 48/96 kHz，合计节省 12 bytes；
- profile 末尾加入合法边界内的 2-byte vendor descriptor `02 ff`，净节省 10 bytes；
- 备用 profile 长度从 `0x00ba` 变为 `0x00b0`；
- 两套 configuration 的共同结束位置仍为 `0x3a07`。

设备 descriptor 报告 `bNumConfigurations=1`，实机枚举的是活动 4-interface configuration；备用 profile 不会作为第二个 configuration 暴露。尽管如此，补丁器仍逐 descriptor 校验该区域的结构完整性。

## 补丁不变量

`patches/patch_v3.py` 只接受 SHA-256 为 `fc162ef47d0b62ede04221d4d0b2f3486935f7a1a609006a96b3e560d63def88` 的官方 base，并验证：

- 已知 Entity 2 播放 volume/mute 和 Entity 5 麦克风 handler signature 唯一存在；
- `0x0010..0x37f7` 的 8051 code/pre-descriptor data 完全不变；
- `0x3a07` 之后的运行时表、字符串和尾部数据完全不变；
- 活动 AudioControl 区精确结束于 Interface 1；
- 两套 configuration 都能按 `bLength` 精确解析到 `wTotalLength`；
- Entity ID 和 source 引用符合目标拓扑；
- ROM 长度不变，header checksum 与重新计算值相等。

输出的固定 SHA-256 为 `af457547495d40ad1e42647a7d76a7f0bd2b757f3b65a1f082876821c5b3d75c`，header checksum 为 `0x6fef`。
