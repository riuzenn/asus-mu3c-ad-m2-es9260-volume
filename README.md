# ASUS MU3C-AD(M2) / ES9260 Apple volume patch

这是针对 ASUS MU3C-AD(M2) USB DAC（USB `0495:3042`）和 ESS ES9260 `3.6.14` 外部固件的可复现 USB Audio descriptor 补丁。v3 在同一活动 configuration 中同时公布真正的播放音量、麦克风增益和 sidetone 控件。

This repository contains a reproducible descriptor patch and guarded Linux `hidraw` tools. It does **not** redistribute ASUS/ESS firmware or device EEPROM dumps.

> [!WARNING]
> 这是非官方实验项目。刷写 EEPROM 存在使设备无法启动、需要 SPI 编程器恢复的风险。工具只针对 VID:PID `0495:3042`、ES9260 `3.6.14` 和已知 ROM 布局；即使型号文字相同，也应先完成两次一致的完整备份。

## v3 做了什么

原固件的播放链路绕过了固件中已经存在的 Feature Unit 2 控制处理代码，因此 Apple 系统不会提供与实际输出增益相连的系统音量控制。v3 仅修改 descriptor 区和 ROM checksum，不改 8051 程序代码：

```text
录音：Headset IT 4 -> Feature Unit 5 -> USB OT 6
侧音：Headset IT 16 -> Feature Unit 7 -> Mixer Unit 8
播放：USB IT 1 -> Feature Unit 2 -> Mixer Unit 8 -> Headset OT 3
```

活动 configuration 从 `0x0155` 扩展至 `0x015f`，AudioControl 从 `0x0060` 扩展至 `0x006a`。FU2 是新增公布的 10-byte descriptor；FU5 麦克风控制和 FU7 sidetone 控制继续保留。详见 [逆向与补丁设计](docs/reverse-engineering.md)。

## 已验证范围

在一只 MU3C-AD(M2) 实机上完成了：

- v3 ROM 结构、checksum 和不变量校验；
- 擦写后的完整 32 KiB EEPROM 逐字节读回；
- 物理断电重插后的 USB Audio/HID 冷启动枚举；
- Linux ALSA 下 FU2 播放、FU5 麦克风和 FU7 sidetone 的可逆 SET/GET 测试。

v2 已在 iPhone 上确认系统播放滑块会改变真实输出；v3 保留同一播放路径并同时恢复麦克风控件，但截至本仓库首次发布，v3 的 iPhone/macOS 实际播放和麦克风增益仍待复测。因此这里不把 Apple 端 v3 行为写成已确认结果。完整边界见 [验证记录](docs/validation.md)。

## 生成 v3 ROM

要求 Python 3，无第三方 Python 依赖。

1. 从 [ASUS 官方包](https://dlcdnets.asus.com/pub/ASUS/nb/UX425EA/ESS_SuiteFW_1.0.0.10.zip)取得 `AppData/ES9260_3.6.14_EXT.rom`。该文件的 SHA-256 必须为：

   ```text
   fc162ef47d0b62ede04221d4d0b2f3486935f7a1a609006a96b3e560d63def88
   ```

2. 运行补丁器：

   ```bash
   python3 patches/patch_v3.py \
     AppData/ES9260_3.6.14_EXT.rom \
     ES9260_3.6.14_EXT_AppleVolumeMic_v3.rom
   ```

3. 生成结果必须为 15,400 bytes，SHA-256 必须为：

   ```text
   af457547495d40ad1e42647a7d76a7f0bd2b757f3b65a1f082876821c5b3d75c
   ```

补丁器会拒绝任何其他输入，并检查已知控制 handler signature、descriptor 边界、实体拓扑、ROM 长度和 checksum。

## Linux 工具

依赖 Linux `hidraw` headers 和 C 编译器：

```bash
make
scripts/find-device.sh
```

生成三个工具：

- `build/eeprom-read`：读取完整 32 KiB EEPROM；
- `build/eeprom-flash`：带预检、staged commit 和完整读回比较的刷写器；
- `build/register-dump`：读取 256-byte USB bridge register snapshot，用于分析。

刷写前必须阅读 [刷写与恢复流程](docs/flashing.md)。推荐入口 `scripts/flash-v3.sh` 还会强制检查 v3 ROM 的 SHA-256。

## Windows 安装包

Windows 10/11 x64 版使用独立 `mu3c-installer.exe`。每次安装先读取、保存并校验两份完整 EEPROM 备份，再由仓库所有者通过 GitHub Actions 对本次随机挑战签发短期 Ed25519 授权。安装命令还会保存一份最新刷前备份、核对实时 EEPROM，并在写入后完成完整读回。完整操作见 [Windows v3 installer](docs/windows.md)。Windows 实机刷写尚待独立验证；此版本保持已验证 v3 的音量范围。

## 安全设计

刷写器会在擦除前执行以下硬检查：

- HID 设备必须报告 `0495:3042`；
- SPI flash type 必须为 `1`；
- ROM 必须为 15,400 bytes、版本 `3.6.14` 且 checksum 正确；
- 实时读取的完整 32 KiB EEPROM 必须与提供的备份逐字节一致。

擦除后先保持 ROM header byte 0 为 `0xff`，只有 staged ROM、保留的设备配置尾部以及完整 32 KiB pre-commit 比较全部通过，才提交 header byte。最后再执行一次完整读回比较并保存结果。这些保护降低风险，但不能消除断电、USB 控制器异常或未知硬件变体带来的风险。

## 仓库内容与版权边界

仓库只发布本项目编写的补丁器、分析文档和 Linux 工具。ASUS、ESS 及产品名称是各自权利人的商标；官方固件仍由其权利人提供并受其条款约束。本项目的 MIT License 仅适用于本仓库中的原创代码与文档，不授予第三方固件的再分发权。
