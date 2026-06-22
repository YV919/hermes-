package main

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/term"
)

// ── 键盘事件类型（console_windows.go / console_other.go 依赖这些符号）──

type KeyType int

const (
	KeyOther KeyType = iota
	KeyUp
	KeyDown
	KeyEnter
	KeyEsc
)

// rawModeState 保存 Unix raw 模式下的原终端状态，供 Ctrl+C 路径恢复（console_windows.go 也会读它）
var rawModeState *term.State

// ── 主题：颜色与图标（applyLegacyTheme 会在老控制台下替换为 ASCII）──

var (
	cReset   = "\033[0m"
	cRed     = "\033[91m"
	cGreen   = "\033[92m"
	cYellow  = "\033[93m"
	cCyan    = "\033[96m"
	cCyanMid = "\033[36m"
	cBlue    = "\033[94m"
	cMagenta = "\033[95m"
	cGray    = "\033[90m"
	cBold    = "\033[1m"
	cDim     = "\033[2m"
)

var (
	iconOK    = "✔"
	iconErr   = "✘"
	iconWarn  = "⚠"
	iconInfo  = "→"
	iconTip   = "◆"
	iconArrow = "❯"
	arrowUp   = "↑"
	arrowDown = "↓"
	// 单线圆角框（菜单用）
	boxTL = "╭"
	boxTR = "╮"
	boxBL = "╰"
	boxBR = "╯"
	boxH  = "─"
	boxV  = "│"
	boxML = "├"
	boxMR = "┤"
	// 双线框（配置摘要用）
	boxDTL = "╔"
	boxDTR = "╗"
	boxDBL = "╚"
	boxDBR = "╝"
	boxDH  = "═"
	boxDV  = "║"
	boxDML = "╠"
	boxDMR = "╣"
)

// applyLegacyTheme 在不支持 ANSI/VT 的老控制台下，把颜色清空、图标降级为 ASCII。
func applyLegacyTheme() {
	cReset, cRed, cGreen, cYellow, cCyan, cCyanMid, cBlue, cMagenta, cGray, cBold, cDim = "", "", "", "", "", "", "", "", "", "", ""
	iconOK, iconErr, iconWarn, iconInfo, iconTip, iconArrow = "[OK]", "[X]", "[!]", "->", "*", ">"
	arrowUp, arrowDown = "^", "v"
	boxTL, boxTR, boxBL, boxBR = "+", "+", "+", "+"
	boxH, boxV = "-", "|"
	boxML, boxMR = "+", "+"
	boxDTL, boxDTR, boxDBL, boxDBR = "+", "+", "+", "+"
	boxDH, boxDV = "-", "|"
	boxDML, boxDMR = "+", "+"
	sectionStart = ">>"
}

// ── CJK 宽度感知 ──

var cjkAmbiguous bool

// detectCJKLocale 检测中日韩 locale，决定 East Asian Ambiguous 字符按宽度 2 渲染。
func detectCJKLocale() {
	for _, env := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		v := strings.ToLower(os.Getenv(env))
		if strings.Contains(v, "zh") || strings.Contains(v, "ja") || strings.Contains(v, "ko") {
			cjkAmbiguous = true
			return
		}
	}
	switch getWindowsACP() {
	case 936, 950, 932, 949: // GBK / Big5 / 日 / 韩
		cjkAmbiguous = true
	}
}

func isAmbiguous(r rune) bool {
	switch r {
	// 注意：'❯' 不在此列——Windows Terminal 按文本展示渲染为宽 1，
	// 若按 ambiguous 判宽 2 会让选中行前缀「❯ 」比未选「  」多 1 列，破坏盒子对齐。
	case '◆', '✔', '✘', '⚠', '↑', '↓', '→', '★', '●', '○':
		return true
	}
	return false
}

// wideEmoji 判定落在 0x1F300 以下、但默认按 emoji 展示（宽 2）渲染的码点
// （Unicode Emoji_Presentation=Yes 的相关区间）。文本展示符如 ❯(276F)/✔(2714) 不在内。
func wideEmoji(r rune) bool {
	switch {
	case r >= 0x231A && r <= 0x231B,
		r >= 0x23E9 && r <= 0x23EC,
		r == 0x23F0, r == 0x23F3,
		r >= 0x25FD && r <= 0x25FE,
		r >= 0x2614 && r <= 0x2615,
		r >= 0x2648 && r <= 0x2653,
		r == 0x267F, r == 0x2693, r == 0x26A1,
		r >= 0x26AA && r <= 0x26AB,
		r >= 0x26BD && r <= 0x26BE,
		r >= 0x26C4 && r <= 0x26C5,
		r == 0x26CE, r == 0x26D4, r == 0x26EA,
		r >= 0x26F2 && r <= 0x26F3,
		r == 0x26F5, r == 0x26FA, r == 0x26FD,
		r == 0x2705,
		r >= 0x270A && r <= 0x270B,
		r == 0x2728, r == 0x274C, r == 0x274E,
		r >= 0x2753 && r <= 0x2755,
		r == 0x2757,
		r >= 0x2795 && r <= 0x2797,
		r == 0x27B0, r == 0x27BF,
		r >= 0x2B1B && r <= 0x2B1C,
		r == 0x2B50, r == 0x2B55:
		return true
	}
	return false
}

// utf8Size 根据 UTF-8 首字节返回该字符的总字节数（1~4）。非法首字节按 1 处理。
// 供 Unix raw 模式逐字节读取时补读续字节用（定义在平台无关文件以便跨平台编译与测试）。
func utf8Size(b byte) int {
	switch {
	case b&0x80 == 0:
		return 1
	case b&0xE0 == 0xC0:
		return 2
	case b&0xF0 == 0xE0:
		return 3
	case b&0xF8 == 0xF0:
		return 4
	default:
		return 1
	}
}

// runeWidth 返回单个字符在终端中的显示宽度（0、1 或 2）。
func runeWidth(r rune) int {
	if r == 0 {
		return 0
	}
	// 变体选择符 / 零宽连接符：本身不占列（VS16 的顶宽在 visibleLength 里对基字处理）
	if r == 0xFE0F || r == 0xFE0E || r == 0x200D {
		return 0
	}
	if (r >= 0x1100 && r <= 0x115F) || // Hangul Jamo
		(r >= 0x2E80 && r <= 0xA4CF) || // CJK 部首 ~ 彝文
		(r >= 0xAC00 && r <= 0xD7A3) || // Hangul Syllables
		(r >= 0xF900 && r <= 0xFAFF) || // CJK 兼容表意
		(r >= 0xFE30 && r <= 0xFE4F) || // CJK 兼容形式
		(r >= 0xFF00 && r <= 0xFF60) || // 全角形式
		(r >= 0xFFE0 && r <= 0xFFE6) ||
		(r >= 0x1F300 && r <= 0x1FAFF) || // emoji
		(r >= 0x20000 && r <= 0x3FFFD) || // CJK 扩展 B+
		wideEmoji(r) { // 0x1F300 以下的 emoji 展示符（➕ ✅ 等）
		return 2
	}
	if cjkAmbiguous && isAmbiguous(r) {
		return 2
	}
	return 1
}

// visibleLength 计算字符串可见宽度，忽略 ANSI 转义码。
// 对变体选择符 U+FE0F（emoji 展示）做顶宽：若前一个基字按文本只占 1 列，则补到 emoji 的 2 列。
func visibleLength(s string) int {
	w := 0
	inEsc := false
	prev := 0 // 上一个可见字符的宽度
	for _, r := range s {
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		if r == '\033' {
			inEsc = true
			continue
		}
		if r == 0xFE0F {
			if prev == 1 { // 文本展示基字被 VS16 提升为 emoji 展示，宽 1→2
				w++
				prev = 2
			}
			continue
		}
		rw := runeWidth(r)
		w += rw
		prev = rw
	}
	return w
}

// ── 彩色输出封装 ──

func printColor(color, text string) { fmt.Printf("%s%s%s\n", color, text, cReset) }
func printSuccess(t string)         { fmt.Printf("%s%s%s %s\n", cGreen, iconOK, cReset, t) }
func printError(t string)           { fmt.Printf("%s%s%s %s\n", cRed, iconErr, cReset, t) }
func printWarning(t string)         { fmt.Printf("%s%s%s %s\n", cYellow, iconWarn, cReset, t) }
func printInfo(t string)            { fmt.Printf("%s%s%s %s\n", cCyan, iconInfo, cReset, t) }
func printTip(t string)             { fmt.Printf("%s%s%s %s\n", cBlue, iconTip, cReset, t) }

// clearScreen 清屏并把光标移到左上角，实现「页面」式导航。
// 非终端（管道/测试）直接返回，避免写入 ANSI 垃圾；legacy 控制台退化为换行滚动。
func clearScreen() {
	if !term.IsTerminal(int(syscall.Stdin)) {
		return
	}
	if legacyConsoleMode {
		fmt.Print(strings.Repeat("\n", 50))
		return
	}
	fmt.Print("\033[2J\033[3J\033[H")
}

// waitKey 显示提示并等待用户按任意键（用于动作结果停留，之后调用方会清屏返回）。
// 非终端直接返回，避免阻塞。
func waitKey(prompt string) {
	if !term.IsTerminal(int(syscall.Stdin)) {
		return
	}
	fmt.Printf("\n%s%s%s", cDim, prompt, cReset)
	readMenuKey()
	fmt.Println()
}

// waitReturn 是动作完成后的统一停留：看清结果再按键返回（调用方随后清屏回上一级）。
func waitReturn() { waitKey("按任意键返回…") }

func printSectionHeader(t string) {
	fmt.Println()
	fmt.Printf("%s%s%s %s%s%s\n", cBlue, sectionStart, cReset, cBold, t, cReset)
}

// sectionStart 是节头前缀；legacy 下由 applyLegacyTheme 降级。
var sectionStart = "┌─"

// maskKey 遮盖密钥：≤8 位返回定长 8 星；否则前 4 + "..." + 后 4。
func maskKey(s string) string {
	r := []rune(s)
	if len(r) <= 8 {
		return "********"
	}
	return string(r[:4]) + "..." + string(r[len(r)-4:])
}

// printConfigSummary 用双线框打印一套配置的摘要（密钥掩码，CJK 宽度感知对齐）。
func printConfigSummary(p Profile) {
	type row struct{ label, value, color string }
	// 有效上下文窗口：模型预设值 > 本配置手填值 > 自动（预设模型固定用预设）。
	eff := presetContextLength(p.Model)
	if eff == 0 {
		eff = p.ContextLength
	}
	ctx := "自动"
	if eff > 0 {
		ctx = fmt.Sprintf("%d", eff)
	}
	rows := []row{
		{"配置名称", p.Name, cBold + cCyan},
		{"Base URL", p.BaseURL, cGreen},
		{"密钥", maskKey(p.APIKey), cYellow},
		{"默认模型", p.Model, cCyan},
		{"上下文窗口", ctx, cMagenta},
		{"仅显示精选", "是（/model 只列精选）", cGreen},
	}
	// 标签列定宽（按显示宽度），右值另计
	labelW := 0
	for _, r := range rows {
		if w := visibleLength(r.label); w > labelW {
			labelW = w
		}
	}
	// 渲染每行内容（不含外框 padding），算最大内容宽度
	lines := make([]string, len(rows))
	contentW := 0
	for i, r := range rows {
		pad := labelW - visibleLength(r.label)
		if pad < 0 {
			pad = 0
		}
		lines[i] = fmt.Sprintf("%s%s%s%s  %s%s%s",
			cBold, r.label, cReset, strings.Repeat(" ", pad),
			r.color, r.value, cReset)
		if w := visibleLength(lines[i]); w > contentW {
			contentW = w
		}
	}
	title := "配置摘要"
	if w := visibleLength(title); w > contentW {
		contentW = w
	}
	inner := contentW + 2 // 左右各 1 空格
	bar := strings.Repeat(boxDH, inner)
	// 顶
	fmt.Printf("\n%s%s%s%s%s\n", cCyan, boxDTL, bar, boxDTR, cReset)
	// 标题居中
	tpad := inner - visibleLength(title)
	if tpad < 0 {
		tpad = 0
	}
	tl := tpad / 2
	fmt.Printf("%s%s%s%s%s%s%s%s%s\n",
		cCyan, boxDV, cReset, strings.Repeat(" ", tl), cBold+title+cReset,
		strings.Repeat(" ", tpad-tl), cCyan, boxDV, cReset)
	// 分隔
	fmt.Printf("%s%s%s%s%s\n", cCyan, boxDML, bar, boxDMR, cReset)
	// 行
	for _, l := range lines {
		rpad := inner - visibleLength(l) - 1
		if rpad < 0 {
			rpad = 0
		}
		fmt.Printf("%s%s%s %s%s%s%s%s\n",
			cCyan, boxDV, cReset, l, strings.Repeat(" ", rpad), cCyan, boxDV, cReset)
	}
	// 底
	fmt.Printf("%s%s%s%s%s\n", cCyan, boxDBL, bar, boxDBR, cReset)
}

func printLogo() {
	logo := []string{
		`██████╗ ███╗   ███╗██╗  ██╗ █████╗ ██████╗ ██╗`,
		`██╔══██╗████╗ ████║╚██╗██╔╝██╔══██╗██╔══██╗██║`,
		`██║  ██║██╔████╔██║ ╚███╔╝ ███████║██████╔╝██║`,
		`██║  ██║██║╚██╔╝██║ ██╔██╗ ██╔══██║██╔═══╝ ██║`,
		`██████╔╝██║ ╚═╝ ██║██╔╝ ██╗██║  ██║██║     ██║`,
		`╚═════╝ ╚═╝     ╚═╝╚═╝  ╚═╝╚═╝  ╚═╝╚═╝     ╚═╝`,
	}
	fmt.Println()
	if legacyConsoleMode {
		// 老控制台无真彩，素色打印（真彩字面量不会被 applyLegacyTheme 清掉，需显式跳过）
		for _, line := range logo {
			fmt.Printf("  %s\n", line)
		}
	} else {
		// Hermes 主题：金黄→橙→琥珀 自上而下 24-bit 真彩渐变
		grad := []string{
			"\033[38;2;255;215;0m",
			"\033[38;2;255;185;0m",
			"\033[38;2;255;160;0m",
			"\033[38;2;255;140;0m",
			"\033[38;2;235;130;30m",
			"\033[38;2;205;110;10m",
		}
		for i, line := range logo {
			fmt.Printf("  %s%s%s%s\n", grad[i], cBold, line, cReset)
		}
	}
	fmt.Println()
	fmt.Printf("  %s%sDMXAPI · Hermes 配置工具  ·  让 AI 触手可及%s\n", cDim, cGray, cReset)
	fmt.Printf("  %sv%s%s  %s/%s/%s%s\n\n", cDim, appVersion, cReset, cMagenta, runtime.GOOS, runtime.GOARCH, cReset)
}

// ── 输入助手 ──
// 所有文本输入（含密钥）统一走这一个 bufio 读取器，避免与菜单的 ReadConsoleInputW
// 或 term.ReadPassword 混用导致预读丢字节 / 在部分 Windows 控制台下读空的问题。

var stdinReader = bufio.NewReader(os.Stdin)

func readLine() string {
	s, err := stdinReader.ReadString('\n')
	if err != nil && s == "" {
		return ""
	}
	return strings.TrimRight(s, "\r\n")
}

// readLineEsc 交互式读取一行，支持 ESC 取消（返回 escaped=true）。masked=true 回显 *。
// 非终端（管道/测试）回退为整行 bufio 读取，永不报告 ESC，保证现有测试与 CI 行为不变。
func readLineEsc(masked bool) (string, bool) {
	if !term.IsTerminal(int(syscall.Stdin)) {
		return readLine(), false
	}
	return readLineRaw(masked)
}

// styledInput 普通文本输入。第二返回值为 true 表示用户按 ESC 取消。
func styledInput(label string) (string, bool) {
	fmt.Printf("%s%s%s %s: ", cCyan, iconArrow, cReset, label)
	v, esc := readLineEsc(false)
	if esc {
		return "", true
	}
	return strings.TrimSpace(v), false
}

// styledInputDefault 带默认值的输入，直接回车则用默认值。ESC 取消时返回 (def, true)。
func styledInputDefault(label, def string) (string, bool) {
	if def != "" {
		fmt.Printf("%s%s%s %s [%s%s%s]: ", cCyan, iconArrow, cReset, label, cGray, def, cReset)
	} else {
		fmt.Printf("%s%s%s %s: ", cCyan, iconArrow, cReset, label)
	}
	v, esc := readLineEsc(false)
	if esc {
		return def, true
	}
	v = strings.TrimSpace(v)
	if v == "" {
		return def, false
	}
	return v, false
}

// readPassword 读取密钥：交互场景下逐字符读取并以 * 回显（真正隐藏），支持 ESC 取消与退格；
// 非交互（管道/测试）场景按整行读取。第二返回值为 true 表示用户按 ESC 取消。
func readPassword(label string) (string, bool) {
	if !term.IsTerminal(int(syscall.Stdin)) {
		fmt.Printf("%s%s%s %s: ", cCyan, iconArrow, cReset, label)
		return strings.TrimSpace(readLine()), false
	}
	fmt.Printf("%s%s%s %s: ", cCyan, iconArrow, cReset, label)
	v, esc := readLineEsc(true)
	if esc {
		return "", true
	}
	return strings.TrimSpace(v), false
}

// ── 菜单：箭头键导航（老控制台降级为数字选择）──

type menuItem struct {
	Label string
	Desc  string
}

// selectMenu 显示一个可上下选择的菜单，返回选中索引；ESC 返回 -1。默认高亮第 0 项。
func selectMenu(title string, items []menuItem) int {
	return selectMenuFrom(title, items, 0)
}

// selectMenuFrom 同 selectMenu，但可指定初始高亮项（用于确认菜单默认"否"等）。
func selectMenuFrom(title string, items []menuItem, defaultIdx int) int {
	if len(items) == 0 {
		return -1
	}
	if defaultIdx < 0 || defaultIdx >= len(items) {
		defaultIdx = 0
	}
	if legacyConsoleMode {
		return selectMenuNumbered(title, items)
	}
	idx := defaultIdx
	drawMenu(title, items, idx, false)
	for {
		switch readMenuKey() {
		case KeyUp:
			idx = (idx - 1 + len(items)) % len(items)
			drawMenu(title, items, idx, true)
		case KeyDown:
			idx = (idx + 1) % len(items)
			drawMenu(title, items, idx, true)
		case KeyEnter:
			return idx
		case KeyEsc:
			return -1
		}
	}
}

// styledConfirm 弹"是/否"确认菜单。返回 (选是, 是否按ESC)。
// ESC→(false,true)：调用方据此返回上一级；否→(false,false)；是→(true,false)。
func styledConfirm(label string, defaultYes bool) (yes, escaped bool) {
	items := []menuItem{
		{Label: "是", Desc: "确认"},
		{Label: "否", Desc: "取消 / 保持不变"},
	}
	def := 1
	if defaultYes {
		def = 0
	}
	idx := selectMenuFrom(label, items, def)
	if idx < 0 {
		return false, true
	}
	return idx == 0, false
}

// drawMenu 以单线圆角盒子渲染菜单：顶部居中标题 + 分隔线 + 各项（标签/描述两列对齐），
// 选中项青色高亮带 ❯，盒子下方一行暗色操作提示。盒子宽度不随选中变化，便于原地重绘。
func drawMenu(title string, items []menuItem, sel int, redraw bool) {
	// 标签列定宽（按可见宽度），描述另起一列对齐
	labelW := 0
	for _, it := range items {
		if w := visibleLength(it.Label); w > labelW {
			labelW = w
		}
	}
	// 预渲染每项内容（含颜色），并算最大可见宽度
	lines := make([]string, len(items))
	contentW := visibleLength(title)
	for i, it := range items {
		prefix := "  "
		label := it.Label
		if i == sel {
			prefix = cCyan + iconArrow + cReset + " "
			label = cCyan + cBold + it.Label + cReset
		}
		pad := labelW - visibleLength(it.Label)
		if pad < 0 {
			pad = 0
		}
		desc := ""
		if it.Desc != "" {
			desc = "  " + cDim + it.Desc + cReset
		}
		lines[i] = prefix + label + strings.Repeat(" ", pad) + desc
		if w := visibleLength(lines[i]); w > contentW {
			contentW = w
		}
	}

	inner := contentW + 2 // 左右各 1 空格
	bar := strings.Repeat(boxH, inner)

	total := len(items) + 5 // 顶 + 标题 + 分隔 + 底 + 提示 + N 项
	if redraw {
		fmt.Printf("\033[%dA", total)
	}

	// 顶
	fmt.Printf("\r\033[K%s%s%s%s%s\n", cCyan, boxTL, bar, boxTR, cReset)
	// 标题居中
	tpad := inner - visibleLength(title)
	if tpad < 0 {
		tpad = 0
	}
	tl := tpad / 2
	fmt.Printf("\r\033[K%s%s%s%s%s%s%s%s%s\n",
		cCyan, boxV, cReset, strings.Repeat(" ", tl), cBold+title+cReset,
		strings.Repeat(" ", tpad-tl), cCyan, boxV, cReset)
	// 分隔
	fmt.Printf("\r\033[K%s%s%s%s%s\n", cCyan, boxML, bar, boxMR, cReset)
	// 各项
	for _, l := range lines {
		rpad := inner - visibleLength(l) - 1
		if rpad < 0 {
			rpad = 0
		}
		fmt.Printf("\r\033[K%s%s%s %s%s%s%s%s\n",
			cCyan, boxV, cReset, l, strings.Repeat(" ", rpad), cCyan, boxV, cReset)
	}
	// 底
	fmt.Printf("\r\033[K%s%s%s%s%s\n", cCyan, boxBL, bar, boxBR, cReset)
	// 提示（盒子外，暗色）
	fmt.Printf("\r\033[K%s  %s/%s 选择 · Enter 确认 · ESC 返回%s\n", cDim, arrowUp, arrowDown, cReset)
}

func selectMenuNumbered(title string, items []menuItem) int {
	fmt.Printf("%s%s%s\n", cBold, title, cReset)
	for i, it := range items {
		desc := ""
		if it.Desc != "" {
			desc = "  " + cDim + it.Desc + cReset
		}
		fmt.Printf("  %d. %s%s\n", i+1, it.Label, desc)
	}
	for {
		v, esc := styledInput("输入序号 (0 返回)")
		if esc || v == "0" || v == "" {
			return -1
		}
		n, err := strconv.Atoi(v)
		if err == nil && n >= 1 && n <= len(items) {
			return n - 1
		}
		printError("无效序号，请重新输入")
	}
}

// readMenuKey 跨平台读取一个导航键。
func readMenuKey() KeyType {
	if runtime.GOOS == "windows" {
		return readConsoleKey()
	}
	oldState, err := term.MakeRaw(int(syscall.Stdin))
	if err != nil {
		// 无法进入 raw 模式（如管道输入），退化为行读取
		line := strings.ToLower(readLine())
		switch line {
		case "", "q":
			return KeyEsc
		case "k", "w":
			return KeyUp
		case "j", "s":
			return KeyDown
		default:
			return KeyEnter
		}
	}
	rawModeState = oldState
	defer func() {
		term.Restore(int(syscall.Stdin), oldState)
		rawModeState = nil
	}()

	buf := make([]byte, 3)
	n, _ := os.Stdin.Read(buf)
	if n == 0 {
		return KeyOther
	}
	switch buf[0] {
	case 3: // Ctrl+C
		term.Restore(int(syscall.Stdin), oldState)
		rawModeState = nil
		restoreConsole()
		fmt.Println()
		os.Exit(130)
	case '\r', '\n':
		return KeyEnter
	case 27: // ESC 或方向键序列
		if n >= 3 && buf[1] == '[' {
			switch buf[2] {
			case 'A':
				return KeyUp
			case 'B':
				return KeyDown
			}
		}
		return KeyEsc
	case 'q', 'Q':
		return KeyEsc
	case 'k', 'K', 'w':
		return KeyUp
	case 'j', 'J', 's':
		return KeyDown
	}
	return KeyOther
}
