//go:build !windows

package main

import (
	"fmt"
	"os"
	"runtime"
	"syscall"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// legacyConsoleMode 在非 Windows 平台始终为 false；主文件中的 enterRawMode 会据此决定是否降级。
var legacyConsoleMode bool

// initWindowsConsole 在非 Windows 平台上只返回一个空的 restore，保持签名一致
func initWindowsConsole() (restore func()) {
	return func() {}
}

// restoreConsole 在非 Windows 上为 no-op，但保留函数以让 raw-mode Ctrl+C 路径两端对称
func restoreConsole() {}

// getWindowsACP 在非 Windows 上返回 0，detectCJKLocale 会忽略此值
func getWindowsACP() uint32 { return 0 }

// broadcastEnvironmentChange 在非 Windows 上为 no-op；仅 Windows 需要 WM_SETTINGCHANGE 广播
func broadcastEnvironmentChange() {}

// readConsoleKey 在非 Windows 平台上是编译占位，运行时永不被调用
func readConsoleKey() KeyType {
	return KeyOther
}

// stdinDataReady 使用 poll(2) 检查 stdin 在指定超时内是否有数据可读。
// 用于区分单独按下 ESC 和以 ESC 开头的方向键序列。
func stdinDataReady(timeoutMs int) bool {
	fds := []unix.PollFd{{Fd: int32(syscall.Stdin), Events: unix.POLLIN}}
	n, err := unix.Poll(fds, timeoutMs)
	return err == nil && n > 0
}

// readLineRaw 在 Unix 终端用 raw 模式逐字节读取一行，支持 ESC 取消、退格与 UTF-8（CJK）输入。
// masked=true 时回显 * 而非字符。无法进入 raw 模式（如管道）时回退整行读取。
// 返回 (文本, 是否被 ESC 取消)。
func readLineRaw(masked bool) (string, bool) {
	oldState, err := term.MakeRaw(int(syscall.Stdin))
	if err != nil {
		return readLine(), false // 管道等非交互场景
	}
	rawModeState = oldState
	defer func() {
		term.Restore(int(syscall.Stdin), oldState)
		rawModeState = nil
	}()

	buf := make([]byte, 1)
	readByte := func() (byte, bool) {
		n, _ := os.Stdin.Read(buf)
		if n == 0 {
			return 0, false
		}
		return buf[0], true
	}

	var runes []rune
	for {
		b, ok := readByte()
		if !ok {
			return string(runes), false
		}
		switch {
		case b == '\r' || b == '\n':
			fmt.Print("\r\n")
			return string(runes), false
		case b == 3: // Ctrl+C
			term.Restore(int(syscall.Stdin), oldState)
			rawModeState = nil
			restoreConsole()
			fmt.Println()
			os.Exit(130)
		case b == 27: // ESC 或方向键转义序列
			if stdinDataReady(1) {
				// 后面还有字节 → 方向键等转义序列，吞掉后继续
				for stdinDataReady(1) {
					readByte()
				}
				continue
			}
			return "", true // 孤立 ESC → 取消
		case b == 127 || b == 8: // 退格
			if len(runes) > 0 {
				w := runeWidth(runes[len(runes)-1])
				runes = runes[:len(runes)-1]
				for i := 0; i < w; i++ {
					fmt.Print("\b \b")
				}
			}
		case b < 0x20:
			// 其它控制字符忽略
		default:
			// 按 UTF-8 首字节长度补读续字节
			size := utf8Size(b)
			bs := make([]byte, 0, 4)
			bs = append(bs, b)
			for i := 1; i < size; i++ {
				cb, ok := readByte()
				if !ok {
					break
				}
				bs = append(bs, cb)
			}
			rr := []rune(string(bs))
			if len(rr) > 0 {
				runes = append(runes, rr[0])
				if masked {
					fmt.Print("*")
				} else {
					fmt.Print(string(rr[0]))
				}
			}
		}
	}
}

// stdinBytesAvailable 使用 FIONREAD ioctl 查询 stdin 内核缓冲区中立即可读的字节数。
// FIONREAD 的 ioctl 编号在 macOS 和 Linux 上不同，通过 runtime.GOOS 区分。
func stdinBytesAvailable() int {
	var req uint
	if runtime.GOOS == "darwin" {
		req = 0x4004667F // macOS FIONREAD（<sys/filio.h>）
	} else {
		req = 0x541B // Linux FIONREAD/TIOCINQ（amd64/arm64）
	}
	n, err := unix.IoctlGetInt(int(syscall.Stdin), req)
	if err != nil {
		return 0
	}
	return n
}
