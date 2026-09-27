# Windows v3 installer

简要流程：下载本仓库 Windows ZIP，并从 [ASUS 官方固件包](https://dlcdnets.asus.com/pub/ASUS/nb/UX425EA/ESS_SuiteFW_1.0.0.10.zip) 单独取得 `AppData/ES9260_3.6.14_EXT.rom`。在 PowerShell 运行 `prepare`，它会两次读取并保存完整 32 KiB EEPROM 备份，生成 `challenge.json`。仓库所有者在 GitHub Actions 手动签发 `permit.json`。拿到许可后运行 `install`；程序还会保存新的刷前备份，并且只允许本次许可对应的设备、EEPROM 状态和 v3 ROM 继续刷写。成功后物理拔插设备并运行 `verify`。下文有完整命令和失败处理。

`mu3c-installer.exe` is a portable Windows 10/11 x64 command-line installer for the already validated ES9260 3.6.14 v3 image. It uses the device's existing HID interface, so it does not replace the USB Audio driver. The Windows transport and installer have automated tests; real Windows hardware flashing still needs a separate end-to-end validation. The v3 firmware's wide `-90..0 dB` volume range is unchanged.

The package contains no ASUS/ESS ROM. Obtain `AppData/ES9260_3.6.14_EXT.rom` from the [ASUS official package](https://dlcdnets.asus.com/pub/ASUS/nb/UX425EA/ESS_SuiteFW_1.0.0.10.zip). Its SHA-256 must be `fc162ef47d0b62ede04221d4d0b2f3486935f7a1a609006a96b3e560d63def88`. The installer patches this exact base in memory and pins the generated v3 ROM to SHA-256 `af457547495d40ad1e42647a7d76a7f0bd2b757f3b65a1f082876821c5b3d75c`.

## Device owner's steps

Connect exactly one ASUS MU3C-AD(M2) (`0495:3042`) directly to the Windows computer. Keep the computer awake and powered throughout the procedure. Open PowerShell in a private working directory and run:

```powershell
.\mu3c-installer.exe prepare -base .\ES9260_3.6.14_EXT.rom -out .\sessions\attempt-001
```

`prepare` checks the exact official base, finds the HID device, requires SPI flash type `1`, and reads the full 32 KiB EEPROM twice. It accepts only a current official 3.6.14, known v2, or v3 ROM. It writes and verifies two separate backups. The session directory must be new; no existing files are overwritten. Keep the entire directory private, especially the `.bin` backups.

Send only `sessions\attempt-001\challenge.json` to the repository owner. It includes a random attempt ID and SHA-256 hashes of the HID path, backup and target ROM; it does not include the raw EEPROM. The owner issues a signed permit for this exact attempt. The permit is valid for four hours after signing and the challenge for at most 24 hours. If it expires, run `prepare` again in a new directory to create fresh backups and a new challenge.

After receiving `permit.json`:

```powershell
.\mu3c-installer.exe install -base .\ES9260_3.6.14_EXT.rom -session .\sessions\attempt-001 -permit .\permit.json
```

The installer verifies the upstream Ed25519 signature, challenge, device identity, target hash and validity window. It reads the EEPROM again, stores a fresh `pre-install.bin`, then rereads the full EEPROM immediately before erase. Any mismatch stops before erase. It stages the ROM with header byte 0 uncommitted, verifies the staged ROM and entire 32 KiB target including the device's saved configuration tail, commits the header and verifies the final full EEPROM image. It saves `post-flash.bin` and a completion receipt. A session can be used for only one installation invocation; even after an error, start with new backups and a new permit.

Physically disconnect and reconnect the DAC. Then run:

```powershell
.\mu3c-installer.exe verify -session .\sessions\attempt-001
```

This independently rereads all 32 KiB after the power cycle and compares it with the final flashing readback. If the device does not enumerate, preserve both backups and the session directory for SPI recovery.

## Upstream owner's permit issuance

The public key is compiled into the installer. Its matching Ed25519 private seed is stored in the repository's `AUTH_SIGNING_SEED` GitHub Actions secret, not in source code or the Windows package. Only a repository writer can manually run the [Issue Windows firmware installation permit](../.github/workflows/issue-permit.yml) workflow. Paste the complete `challenge.json` into the `challenge_json` workflow input, run it on `main`, then download its `permit-<run id>` artifact and send only `permit.json` back to the device owner. The workflow validates the pinned v3 hash and challenge lifetime before signing.

Each `prepare` creates a fresh random attempt ID. A permit for another attempt, another EEPROM state or another device fails verification. `install` requires a saved backup and valid permit; copying only the executable cannot authorize a flash. This is a short-lived signed owner approval, not GitHub OAuth authentication for the end user. A user with arbitrary code execution on their own Windows machine can still modify their local executable; this workflow does not claim to enforce policy against such an attacker.

## Build from source

Go 1.23 or newer is sufficient. From this repository:

```powershell
go test ./...
go build -trimpath -ldflags="-s -w" -o mu3c-installer.exe ./cmd/mu3c-installer
```

The `Build Windows v3 installer` GitHub Actions workflow also creates a ZIP containing `mu3c-installer.exe` and this document. The executable is not Authenticode signed; the firmware installation permit uses a separate Ed25519 signature.
