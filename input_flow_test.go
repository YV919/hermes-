package main

import (
	"bufio"
	"strings"
	"testing"
)

// 验证统一输入链：密钥这一步能读到完整非空值（回归测试 term.ReadPassword 读空的 bug）
func TestReadPasswordReadsValue(t *testing.T) {
	stdinReader = bufio.NewReader(strings.NewReader("my-base\nsk-REALKEY123\n"))
	base := readLine()
	if base != "my-base" {
		t.Fatalf("base 读取错误: %q", base)
	}
	key := strings.TrimSpace(readLine())
	if key != "sk-REALKEY123" {
		t.Fatalf("密钥读取错误，得到 %q（旧 bug 会得到空串）", key)
	}
}

func TestReadLineHandlesNoTrailingNewline(t *testing.T) {
	stdinReader = bufio.NewReader(strings.NewReader("sk-last"))
	if v := readLine(); v != "sk-last" {
		t.Fatalf("无结尾换行时读取错误: %q", v)
	}
}

// 非终端（测试/管道）下 readLineEsc 必须走整行回退，且永不报告 ESC。
func TestReadLineEscNonTerminal(t *testing.T) {
	stdinReader = bufio.NewReader(strings.NewReader("hello\n"))
	v, esc := readLineEsc(false)
	if esc {
		t.Fatalf("非终端不应报告 ESC")
	}
	if v != "hello" {
		t.Fatalf("读取错误: %q", v)
	}
}

// 非终端（测试/管道）下清屏与等待按键必须立即返回，不阻塞、不依赖 TTY。
func TestClearScreenWaitKeyNonTerminalSafe(t *testing.T) {
	clearScreen()
	waitKey("x")
	waitReturn()
}

// 校验字符显示宽度与 Windows Terminal 渲染一致，保证菜单盒子对齐。
func TestRuneWidthEmoji(t *testing.T) {
	wCases := map[rune]int{
		0x2795: 2,   // ➕
		0x2705: 2,   // ✅
		0x1F680: 2,  // 🚀
		'❯':    1,   // 文本展示，宽 1
		'中':    2,
		'a':     1,
		0xFE0F: 0,   // 变体选择符不占列
	}
	for r, want := range wCases {
		if got := runeWidth(r); got != want {
			t.Errorf("runeWidth(U+%04X)=%d want %d", r, got, want)
		}
	}
	vCases := map[string]int{
		"✏️":      2, // 270F + FE0F → 顶宽到 2
		"🗑️":      2, // 1F5D1 + FE0F → 2
		"❯ 测试":   6, // 1 + 1 + 2 + 2
	}
	for s, want := range vCases {
		if got := visibleLength(s); got != want {
			t.Errorf("visibleLength(%q)=%d want %d", s, got, want)
		}
	}
}

func TestUTF8Size(t *testing.T) {
	cases := map[byte]int{
		'a':  1,    // ASCII
		0xC3: 2,    // 2 字节序列首字节（如 é）
		0xE4: 3,    // 3 字节序列首字节（中文，如 "中" = E4 B8 AD）
		0xF0: 4,    // 4 字节序列首字节（emoji）
		0x80: 1,    // 续字节（非法首字节）按 1 处理
	}
	for b, want := range cases {
		if got := utf8Size(b); got != want {
			t.Errorf("utf8Size(0x%02X)=%d want %d", b, got, want)
		}
	}
}
