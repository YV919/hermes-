# 项目说明

## 发布新版本（推送新 Tag）前必须同步修改的版本号

当用户说"发布/推送新 tag"或"升级版本"时，以下文件的版本号**必须全部同步更新**：

| 文件 | 位置 | 格式示例 | 说明 |
|------|------|----------|------|
| `main.go` | 第 12 行 `appVersion` 常量 | `"1.0.0"` | 不带 `v` 前缀 |
| `install.sh` | 文件顶部 `VERSION` 变量 | `"v1.0.0"` | 带 `v` 前缀 |
| `install.ps1` | 文件顶部 `$VERSION` 变量 | `"v1.0.0"` | 带 `v` 前缀 |
| `install.cmd` | 文件顶部 `VERSION` 变量 | `v1.0.0` | 带 `v` 前缀 |

> **注意**：Go 源码中版本号不带 `v`（如 `"1.0.0"`），安装脚本中带 `v`（如 `"v1.0.0"`），两者格式不同，请勿混淆。

## 这是多文件 Go 包，注意编译方式

本项目由多个源文件组成同一个 `main` 包：`main.go`、`ui.go`、`hermes.go`、`store.go`、`console_windows.go`、`console_other.go`。

因此编译时**必须编译整个当前目录这个包**，用 `.` 作为构建目标，而不能只指定单个 `.go` 文件：

```bash
go build -o dmxapi-hermes .
```

> 严禁写成 `go build -o dmxapi-hermes main.go` 这类单文件形式——那样会因缺少同包其它文件中的符号而编译失败。Windows 输出名加 `.exe`。

## 发布流程

修改完上述版本号文件后，按以下顺序发布：

1. 改版本号（main.go + install.sh + install.ps1 + install.cmd 全部同步）
2. `commit`
3. 打 tag：`tag v<版本>`（如 `v1.0.0`）
4. `push`（务必包含 tags，如 `git push --tags`）
5. cnb.cool CI 自动完成跨平台编译与发布
