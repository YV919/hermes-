# DMXAPI Hermes 配置工具

一键配置 Hermes Agent (Nous Research) 使用 DMXAPI 的跨平台终端工具，支持多套配置一键切换。

## 功能特性

- 交互式新增配置：填写 配置名 / base_url / 密钥 / 模型 / 上下文窗口，自动校验后保存
- 多套命名配置管理：编辑、删除、一键切换
- 直接改写 Hermes 配置文件（无需手动设置环境变量）
- 写入前自动备份原配置
- 环境自检
- 支持 Windows / Linux / macOS

## ⚡ 快速安装（推荐）

无需手动下载，一行命令完成安装并自动启动配置。

### Linux / macOS

```bash
curl -fsSL https://cnb.cool/dmxapi/dmxapi_hermes/-/git/raw/main/install.sh | bash
```

### Windows PowerShell

```powershell
iwr -useb https://cnb.cool/dmxapi/dmxapi_hermes/-/git/raw/main/install.ps1 | iex
```

### Windows CMD

```cmd
curl -fsSL https://cnb.cool/dmxapi/dmxapi_hermes/-/git/raw/main/install.cmd -o "%TEMP%\install.cmd" && call "%TEMP%\install.cmd"
```

> **Windows 说明**：CMD 方案需要 Windows 10 版本 1803 或更高（内置 curl）。

---

## 下载

> **说明**：`[版本]` 替换为实际下载的版本号，如 `v1.0.0`

| 平台 | 架构 | 文件名 |
|------|------|--------|
| Windows | x64 | `dmxapi-hermes-[版本]-windows-amd64.exe` |
| Linux | x64 | `dmxapi-hermes-[版本]-linux-amd64` |
| Linux | ARM64 | `dmxapi-hermes-[版本]-linux-arm64` |
| macOS | Intel | `dmxapi-hermes-[版本]-macos-amd64` |
| macOS | Apple Silicon (M1/M2/M3/M4) | `dmxapi-hermes-[版本]-macos-arm64` |

## 快速选择版本

不确定自己的系统架构？运行以下命令确认：

| 系统 | 检测命令 | 结果 → 对应文件后缀 |
|------|----------|---------------------|
| Windows | `echo %PROCESSOR_ARCHITECTURE%` | `AMD64` → `windows-amd64.exe` |
| Linux | `uname -m` | `x86_64` → `linux-amd64` / `aarch64` → `linux-arm64` |
| macOS | `uname -m` | `x86_64` → `macos-amd64` / `arm64` → `macos-arm64` |

## 使用方法

> **说明**：以下示例文件名中的 `v1.0.0` 为版本号示例，请替换为实际下载的版本号。

### Windows x64

```powershell
# 下载后直接运行
.\dmxapi-hermes-v1.0.0-windows-amd64.exe
```

### Linux

#### Linux x64 (amd64)

适用于普通 PC 服务器、云主机（x86_64 架构）。

```bash
# 确认架构
uname -m  # 应输出 x86_64

# 添加执行权限
chmod +x dmxapi-hermes-v1.0.0-linux-amd64

# 运行
./dmxapi-hermes-v1.0.0-linux-amd64
```

#### Linux ARM64

适用于树莓派（64 位系统）、AWS Graviton、Oracle Ampere 等 ARM64 架构服务器。

```bash
# 确认架构
uname -m  # 应输出 aarch64

# 添加执行权限
chmod +x dmxapi-hermes-v1.0.0-linux-arm64

# 运行
./dmxapi-hermes-v1.0.0-linux-arm64
```

### macOS

#### macOS Apple Silicon (M1/M2/M3/M4，arm64)

适用于 2020 年末及之后发布的 Mac（搭载 Apple Silicon 芯片）。

```bash
# 确认架构
uname -m  # 应输出 arm64

# 添加执行权限并移除 macOS 安全隔离标记
chmod +x dmxapi-hermes-v1.0.0-macos-arm64
xattr -cr dmxapi-hermes-v1.0.0-macos-arm64

# 运行
./dmxapi-hermes-v1.0.0-macos-arm64
```

#### macOS Intel (amd64)

适用于 2020 年前发布的 Mac（搭载 Intel 处理器）。

```bash
# 确认架构
uname -m  # 应输出 x86_64

# 添加执行权限并移除 macOS 安全隔离标记
chmod +x dmxapi-hermes-v1.0.0-macos-amd64
xattr -cr dmxapi-hermes-v1.0.0-macos-amd64

# 运行
./dmxapi-hermes-v1.0.0-macos-amd64
```

> **说明**：`xattr -cr` 用于移除 macOS 对从网络下载文件添加的隔离标记（`com.apple.quarantine`），是 macOS 运行未签名可执行文件的必要步骤。若跳过此步骤，系统可能提示"无法验证开发者"或"已损坏，无法打开"。

## 交互式界面

直接运行程序即进入交互式界面，可完成全部日常操作：

- **新增配置**：依次填写 配置名、base_url、API 密钥、模型、上下文窗口（context_length），工具会自动校验连接有效性，校验通过后保存；保存后可选择是否设为当前生效配置。
- **编辑配置**：修改某套已保存配置的字段。
- **删除配置**：移除不再需要的配置。
- **一键切换**：在多套已保存配置之间切换当前生效配置。
- **环境自检**：检查 Hermes 配置文件位置与当前生效情况。

## 非交互命令

无需进入交互界面，可直接通过命令行参数操作：

```bash
dmxapi-hermes --list            # 列出已保存的配置 + 当前生效
dmxapi-hermes --switch <配置名>  # 一键切换到某套已保存配置
dmxapi-hermes --version         # 显示版本
dmxapi-hermes --help            # 显示帮助
```

## 配置如何生效

本工具**不写环境变量**，而是直接修改 Hermes 的配置文件：

- Windows：`%LOCALAPPDATA%\hermes\config.yaml`
- 实际路径以 `hermes config path` 的输出为准（工具会优先调用它定位，失败时回退到平台默认位置）。

写入时，工具会把 `model` 块改为内联的 `provider: custom` + `base_url` + `api_key` + `default`，并 upsert 对应的 `custom_providers` 条目以承载 `context_length`。

每次写入前，原配置会自动备份到 `~/.DMXAPI/hermes/backups/`。

配置改完后，**下次运行 `hermes` 时自动生效，无需重启终端或重新登录**。

## 常见问题

**Q：macOS 提示"无法验证开发者"或"已损坏，无法打开"**

A：这是 macOS Gatekeeper 的安全机制，并非文件损坏。按照上方安装步骤执行 `xattr -cr <文件名>` 移除隔离标记后重新运行即可。若已错过此步骤，单独执行以下命令：

```bash
xattr -cr dmxapi-hermes-v1.0.0-macos-arm64  # Apple Silicon
# 或
xattr -cr dmxapi-hermes-v1.0.0-macos-amd64  # Intel
```

---

**Q：Linux 运行时提示 `Permission denied`**

A：缺少执行权限，运行以下命令后再重试：

```bash
chmod +x <文件名>
```

---

**Q：配置后 Hermes 起不来，报缺少 `concurrent_log_handler`**

A：这是 Hermes 运行环境缺少日志依赖，与本工具的配置无关。进入 Hermes 所在的 Python 环境补装即可：

```bash
# 进入 Hermes 的 venv 后执行
python -m ensurepip
pip install concurrent-log-handler
```

装好后重新运行 `hermes` 即可。

---

**Q：如何确认我的 Mac 是 Intel 还是 Apple Silicon？**

A：运行 `uname -m`，输出 `arm64` 为 Apple Silicon，输出 `x86_64` 为 Intel。也可点击苹果菜单 → **关于本机**，在"芯片"或"处理器"行查看。

---

**Q：树莓派用哪个版本？**

A：使用 `linux-arm64` 版本（需确保系统为 64 位，运行 `uname -m` 应输出 `aarch64`）。32 位系统暂不支持。

## 从源码编译

```bash
# 安装 Go 1.21+
# https://go.dev/dl/

# 下载依赖
go mod tidy

# 编译当前平台（本项目为多文件 Go 包，编译当前目录 "."）
go build -o dmxapi-hermes .

# 交叉编译其他平台
GOOS=linux  GOARCH=amd64 go build -o dmxapi-hermes-linux-amd64 .
GOOS=linux  GOARCH=arm64 go build -o dmxapi-hermes-linux-arm64 .
GOOS=darwin GOARCH=amd64 go build -o dmxapi-hermes-macos-amd64 .
GOOS=darwin GOARCH=arm64 go build -o dmxapi-hermes-macos-arm64 .
```

## 获取 Token

访问 [https://www.dmxapi.cn/token](https://www.dmxapi.cn/token) 获取您的 API Token。

## 许可证

MIT License
