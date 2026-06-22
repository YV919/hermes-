package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// 发布前需同步修改的版本号（见 CLAUDE.md）：本常量不带 v 前缀。
const (
	appVersion         = "1.0.0"
	appName            = "DMXAPI Hermes 配置工具"
	recommendedBaseURL = "https://www.dmxapi.cn/v1"
	tokenURL           = "https://www.dmxapi.cn/token"
	// 推荐配置（一键）的预设：用户只需补密钥。
	recommendedProfileName = "DMXAPI推荐配置"
	recommendedModel       = "deepseek-v4-pro"
)

// presetModel 描述精选清单里的一个模型：ID 写入配置，Hint 仅用于显示。
type presetModel struct {
	ID   string
	Hint string
}

// presetModels 是 DMXAPI 常用对话/编码模型精选清单。
// 仅收录有来源佐证的模型名（依据 dmxapi.doc 请求体 / 实测），DMXAPI 新增模型时用"自定义输入"兜底。
var presetModels = []presetModel{
	{"deepseek-v4-flash", "深度求索 · 快速便宜"},
	{"deepseek-v4-pro", "深度求索 · 思考主推"},
	{"claude-sonnet-4-6", "Claude · 均衡"},
	{"claude-opus-4-7", "Claude · 最强"},
	{"gpt-5.4", "OpenAI · 新版"},
	{"gpt-5.2", "OpenAI"},
	{"gpt-5.1", "OpenAI"},
	{"gpt-5-mini", "OpenAI · 轻量"},
}

type action int

const (
	actManage action = iota
	actAdd
	actRecommend
	actCheck
	actClear
	actQuit
)

func main() {
	restore := initWindowsConsole()
	defer restore()
	detectCJKLocale()

	if len(os.Args) > 1 && handleCLI(os.Args[1:]) {
		return
	}

	configPath, cfgErr := locateHermesConfig()

	for {
		renderHome(configPath, cfgErr)

		profiles, _ := listProfiles()
		var items []menuItem
		var acts []action
		var rowProfiles []Profile // 与 items/acts 同长；非配置行填零值 Profile
		add := func(label, desc string, a action, p Profile) {
			items = append(items, menuItem{Label: label, Desc: desc})
			acts = append(acts, a)
			rowProfiles = append(rowProfiles, p)
		}

		// 顺序：推荐置顶 → 已保存配置 → 新增 → 其余
		add("🚀 DMXAPI 推荐配置", "预填名称/地址/模型，只需补密钥", actRecommend, Profile{})
		for _, p := range profiles {
			add(p.Name, p.Model+" · "+hostOf(p.BaseURL), actManage, p)
		}
		add("➕ 新增配置", "填 base_url / 密钥 / 模型 / 上下文窗口", actAdd, Profile{})
		add("🩺 环境自检", "检查 Hermes 能否运行", actCheck, Profile{})
		add("🧹 清除配置", "清除当前生效 / 全部配置 / 某个已保存配置", actClear, Profile{})
		add("退出", "", actQuit, Profile{})

		choice := selectMenu("请选择操作：", items)
		if choice < 0 {
			continue // 主菜单按 ESC 不退出，停留并重画
		}
		switch acts[choice] {
		case actManage:
			manageProfileFlow(configPath, rowProfiles[choice])
		case actAdd:
			addConfigFlow(configPath, false)
		case actRecommend:
			addConfigFlow(configPath, true)
		case actCheck:
			envSelfCheck()
		case actClear:
			clearConfigFlow(configPath)
		case actQuit:
			fmt.Println("再见 👋")
			return
		}
	}
}

// renderHome 清屏并重画主菜单页头部：LOGO + 配置文件路径 + 当前生效。每次回到主菜单都调用。
func renderHome(configPath string, cfgErr error) {
	clearScreen()
	printLogo()
	if cfgErr != nil {
		printWarning(cfgErr.Error())
	} else if configPath != "" {
		printInfo("Hermes 配置文件：" + configPath)
	}
	if configPath != "" {
		if model, provider, _ := readActiveModel(configPath); model != "" {
			fmt.Println()
			printTip(fmt.Sprintf("当前生效：模型 %s（provider=%s）", model, provider))
		}
	}
}

// handleCLI 处理非交互命令行参数，返回 true 表示已处理完毕（应退出）。
func handleCLI(args []string) bool {
	switch args[0] {
	case "--version", "-v", "version":
		fmt.Printf("%s v%s\n", appName, appVersion)
		return true
	case "--help", "-h", "help":
		printHelp()
		return true
	case "--list", "list":
		if configPath, _ := locateHermesConfig(); configPath != "" {
			if m, prov, _ := readActiveModel(configPath); m != "" {
				printTip(fmt.Sprintf("当前生效：%s（provider=%s）", m, prov))
			}
		}
		profiles, _ := listProfiles()
		if len(profiles) == 0 {
			printInfo("还没有已保存的配置。运行不带参数进入交互式新增。")
			return true
		}
		for _, p := range profiles {
			fmt.Printf("  • %s  (%s · %s)\n", p.Name, p.Model, hostOf(p.BaseURL))
		}
		return true
	case "--switch", "switch":
		if len(args) < 2 {
			printError("用法：dmxapi-hermes --switch <配置名>")
			return true
		}
		configPath, err := locateHermesConfig()
		if err != nil {
			printError(err.Error())
			return true
		}
		p, err := loadProfile(args[1])
		if err != nil {
			printError("找不到配置：" + args[1])
			return true
		}
		applyAndReport(configPath, p)
		return true
	}
	return false
}

func printHelp() {
	fmt.Printf("%s v%s\n\n", appName, appVersion)
	fmt.Println("用法：")
	fmt.Println("  dmxapi-hermes              进入交互式界面（新增/切换/编辑/删除配置）")
	fmt.Println("  dmxapi-hermes --list       列出已保存的配置 + 当前生效")
	fmt.Println("  dmxapi-hermes --switch <名> 一键切换到某套已保存配置")
	fmt.Println("  dmxapi-hermes --version    显示版本")
	fmt.Println("  dmxapi-hermes --help       显示本帮助")
}

// addConfigFlow 新增一套配置（recommended=true 时预填 DMXAPI 地址）。
func addConfigFlow(configPath string, recommended bool) {
	clearScreen()

	var name, baseURL, model, apiKey string
	var ctx int

	if recommended {
		// 一键：名称 / 地址 / 模型 全部预设，只问密钥。
		printSectionHeader("DMXAPI 推荐配置")
		name = recommendedProfileName
		baseURL = recommendedBaseURL
		model = recommendedModel
		ctx = 0
		printInfo("已为你预填以下配置，只需填写密钥：")
		printInfo("配置名称：" + name)
		printInfo("Base URL：" + baseURL)
		printInfo("模型：" + model)

		printSectionHeader("配置 API 认证令牌")
		printTip("获取地址: " + tokenURL)
		key, esc := mustPassword("API 密钥 (sk-...)")
		if esc {
			return
		}
		apiKey = key
	} else {
		printSectionHeader("新增 DMXAPI 配置")

		n, esc := mustInput("配置名称（如 flash日常）")
		if esc {
			return
		}
		name = n

		printSectionHeader("配置 API 服务器地址")
		fmt.Println("  示例: " + recommendedBaseURL)
		baseURLraw, esc := styledInputDefault("Base URL", recommendedBaseURL)
		if esc {
			return
		}
		baseURL = normalizeBaseURL(baseURLraw)
		if baseURL == "" {
			baseURL = recommendedBaseURL
		}

		printSectionHeader("配置 API 认证令牌")
		printTip("获取地址: " + tokenURL)
		key, esc := mustPassword("API 密钥 (sk-...)")
		if esc {
			return
		}
		apiKey = key

		printSectionHeader("配置模型")
		m := pickModel()
		if m == "" {
			return
		}
		model = m

		c, esc := askContextLength("")
		if esc {
			return
		}
		ctx = c
	}

	p := Profile{Name: name, BaseURL: baseURL, APIKey: apiKey, Model: model, ContextLength: ctx}

	printInfo("正在校验密钥与模型...")
	if err := validateChat(baseURL, apiKey, model); err != nil {
		printWarning("校验未通过：" + err.Error())
		if !styledConfirm("仍然保存此配置吗？", false) {
			return
		}
	} else {
		printSuccess("校验通过：密钥有效、模型可用 ✔")
	}

	if err := saveProfile(p); err != nil {
		printError("保存失败：" + err.Error())
		waitReturn()
		return
	}
	printSuccess("配置已保存：" + name)

	printConfigSummary(p)
	if styledConfirm("是否立即设为 Hermes 当前生效？", true) {
		applyAndReport(configPath, p)
	} else {
		printWarning("配置已保存，但尚未生效——Hermes 仍在用旧配置。")
		printInfo("随时可在主菜单选中本配置→应用此配置，或运行：dmxapi-hermes --switch " + p.Name)
	}
	waitReturn()
}

// manageProfileFlow 管理一套已保存配置：显示摘要 + 应用/编辑/删除。
func manageProfileFlow(configPath string, p Profile) {
	clearScreen()
	printSectionHeader("配置：" + p.Name)
	printConfigSummary(p)
	idx := selectMenu("管理配置「"+p.Name+"」", []menuItem{
		{Label: "✅ 应用此配置", Desc: "设为 Hermes 当前生效"},
		{Label: "✏️  编辑此配置", Desc: "修改 base_url / 密钥 / 模型 / 上下文"},
		{Label: "🗑️  删除此配置", Desc: "从配置库移除"},
	})
	switch idx {
	case 0:
		switchToProfile(configPath, p)
		waitReturn()
	case 1:
		editProfile(configPath, p)
	case 2:
		if !styledConfirm("确定删除「"+p.Name+"」吗？", false) {
			return
		}
		if err := deleteProfile(p.Name); err != nil {
			printError("删除失败：" + err.Error())
		} else {
			printSuccess("已删除：" + p.Name)
		}
		waitReturn()
	default: // ESC：返回主菜单
		return
	}
}

// editProfile 编辑单套配置：每个字段走"显示当前值 + 是否修改"模式。
func editProfile(configPath string, p Profile) {
	clearScreen()
	printSectionHeader("配置 API 服务器地址")
	fmt.Println("  示例: " + recommendedBaseURL)
	fmt.Println("  当前值: " + p.BaseURL)
	if styledConfirm("是否修改 Base URL", false) {
		v, esc := mustInput("Base URL")
		if esc {
			return
		}
		p.BaseURL = normalizeBaseURL(v)
	}

	printSectionHeader("配置 API 认证令牌")
	printTip("获取地址: " + tokenURL)
	fmt.Println("  当前已配置 Token: " + maskKey(p.APIKey))
	if styledConfirm("是否更新 Token", false) {
		v, esc := mustPassword("API 密钥 (sk-...)")
		if esc {
			return
		}
		p.APIKey = v
	}

	printSectionHeader("配置模型")
	fmt.Println("  当前: " + p.Model)
	if styledConfirm("是否修改模型", false) {
		m := pickModel()
		if m == "" {
			return
		}
		p.Model = m
	}

	printSectionHeader("配置上下文窗口")
	cur := "自动"
	if p.ContextLength > 0 {
		cur = strconv.Itoa(p.ContextLength)
	}
	fmt.Println("  当前: " + cur)
	if styledConfirm("是否修改上下文窗口", false) {
		n, esc := askContextLength(strconv.Itoa(p.ContextLength))
		if esc {
			return
		}
		p.ContextLength = n
	}

	printInfo("正在校验密钥与模型...")
	if err := validateChat(p.BaseURL, p.APIKey, p.Model); err != nil {
		printWarning("校验未通过：" + err.Error())
		if !styledConfirm("仍然保存此修改吗？", false) {
			return
		}
	} else {
		printSuccess("校验通过：密钥有效、模型可用 ✔")
	}

	if err := saveProfile(p); err != nil {
		printError("保存失败：" + err.Error())
		waitReturn()
		return
	}
	printSuccess("已保存修改。")
	printConfigSummary(p)
	if styledConfirm("立即应用到 Hermes？", true) {
		applyAndReport(configPath, p)
	} else {
		printWarning("修改已保存，但尚未生效——Hermes 仍在用旧配置。")
		printInfo("随时可在主菜单选中本配置→应用此配置，或运行：dmxapi-hermes --switch " + p.Name)
	}
	waitReturn()
}

// switchToProfile 一键把某套配置设为 Hermes 当前生效。
func switchToProfile(configPath string, p Profile) {
	if configPath == "" {
		printError("未找到 Hermes 配置文件，无法切换。")
		return
	}
	applyAndReport(configPath, p)
}

func applyAndReport(configPath string, p Profile) {
	if configPath == "" {
		printError("未找到 Hermes 配置文件，无法写入。")
		return
	}
	backup, err := applyProfileToConfig(configPath, p)
	if err != nil {
		printError("写入 Hermes 配置失败：" + err.Error())
		return
	}
	printSuccess(fmt.Sprintf("已切换到「%s」：模型 %s", p.Name, p.Model))
	printInfo("已备份原配置：" + backup)
	printInfo("下次运行 hermes 即生效（无需重启）。")
	printInfo("Hermes 的 /model 现在会显示这套配置的精选模型（含你的自定义模型）。")
	printTip("若 Hermes 正开着，重开会话或运行 hermes model --refresh 刷新模型列表。")
}

// clearConfigFlow 清除配置子菜单：清除当前生效 / 清除所有 / 清除某个已保存配置。
func clearConfigFlow(configPath string) {
	clearScreen()
	printSectionHeader("清除配置")
	idx := selectMenu("请选择清除方式：", []menuItem{
		{Label: "清除当前配置", Desc: "仅清除当前生效配置，保留已保存配置（用于切换/登录订阅账号）"},
		{Label: "清除所有配置", Desc: "删除全部命名配置 + 清除 Hermes 配置"},
		{Label: "清除用户新增配置", Desc: "选择并删除某个已保存的命名配置"},
	})
	switch idx {
	case 0:
		clearCurrentFlow(configPath)
	case 1:
		clearAllFlow(configPath)
	case 2:
		clearOneProfileFlow()
	default: // ESC：返回主菜单
		return
	}
}

// clearCurrentFlow 仅清除当前生效配置（移除 Hermes config 的 model 块），保留已保存配置。
func clearCurrentFlow(configPath string) {
	if configPath == "" {
		printError("未找到 Hermes 配置文件，无法清除。")
		waitReturn()
		return
	}
	if model, _, _ := readActiveModel(configPath); model == "" {
		printInfo("当前没有生效的 model 配置，无需清除。")
		waitReturn()
		return
	}
	printWarning("将清除当前生效配置（model 块），已保存的命名配置保留。")
	if !styledConfirm("确定清除当前生效配置吗？", false) {
		return
	}
	backup, err := clearActiveModel(configPath)
	if err != nil {
		printError("清除失败：" + err.Error())
		waitReturn()
		return
	}
	printSuccess("已清除当前生效配置。")
	printInfo("已备份原配置：" + backup)
	printInfo("Hermes 将回退到默认模型选择。")
	waitReturn()
}

// clearAllFlow 清除所有配置：删全部命名配置 + 清除 Hermes 配置（model 块 + 本工具写入的 custom_providers 条目）。
func clearAllFlow(configPath string) {
	profiles, _ := listProfiles()
	printWarning(fmt.Sprintf("将删除全部 %d 套已保存配置，并清除 Hermes 当前生效配置 + 本工具写入的 custom_providers 条目。", len(profiles)))
	printWarning("此操作不可撤销（Hermes 配置会自动备份）。")
	if !styledConfirm("确定清除所有配置吗？", false) {
		return
	}

	// 1) 清 Hermes 配置：用已保存 profile 的 base_url 匹配删除对应 custom_providers 条目
	if configPath != "" {
		var urls []string
		seen := map[string]bool{}
		for _, p := range profiles {
			if p.BaseURL != "" && !seen[p.BaseURL] {
				seen[p.BaseURL] = true
				urls = append(urls, p.BaseURL)
			}
		}
		backup, err := clearToolConfig(configPath, urls)
		if err != nil {
			printError("清除 Hermes 配置失败：" + err.Error())
		} else {
			printSuccess("已清除 Hermes 配置（model 块 + 本工具的 custom_providers 条目）。")
			printInfo("已备份原配置：" + backup)
		}
	} else {
		printWarning("未找到 Hermes 配置文件，跳过清除 Hermes 配置。")
	}

	// 2) 删全部命名配置（与上一步独立，各自报告成败）
	n, err := deleteAllProfiles()
	if err != nil {
		printError("删除命名配置失败：" + err.Error())
		waitReturn()
		return
	}
	printSuccess(fmt.Sprintf("已删除 %d 套命名配置。", n))
	waitReturn()
}

// clearOneProfileFlow 选择并删除某个已保存的命名配置（仅删 JSON，不动 Hermes 配置）。
func clearOneProfileFlow() {
	profiles, _ := listProfiles()
	if len(profiles) == 0 {
		printInfo("还没有已保存的配置。")
		waitReturn()
		return
	}
	items := make([]menuItem, 0, len(profiles))
	for _, p := range profiles {
		items = append(items, menuItem{Label: p.Name, Desc: p.Model + " · " + hostOf(p.BaseURL)})
	}
	idx := selectMenu("选择要删除的配置：", items)
	if idx < 0 {
		return
	}
	p := profiles[idx]
	if !styledConfirm("确定删除「"+p.Name+"」吗？", false) {
		return
	}
	if err := deleteProfile(p.Name); err != nil {
		printError("删除失败：" + err.Error())
	} else {
		printSuccess("已删除：" + p.Name)
	}
	waitReturn()
}

// envSelfCheck 检查 Hermes 是否能正常运行，并对已知依赖坑给出修复指引。
func envSelfCheck() {
	clearScreen()
	printSectionHeader("Hermes 环境自检")

	if out, err := runHermesCombined("config", "check"); err == nil {
		printInfo("hermes config check（节选）：")
		fmt.Println(firstLines(out, 10))
	} else {
		printWarning("无法运行 hermes config check：" + err.Error())
	}

	printInfo("正在用 hermes -z 冒烟测试（需几秒）...")
	out, err := runHermesCombined("-z", "ping")
	combined := out + " " + errString(err)
	if err != nil {
		printError("Hermes 运行失败。")
		fmt.Println(firstLines(out, 6))
		if strings.Contains(combined, "concurrent_log_handler") || strings.Contains(combined, "No module named") {
			printWarning("疑似缺少 Python 依赖。修复（复制到终端执行）：")
			fmt.Println("  \"" + hermesVenvPython() + "\" -m ensurepip --upgrade")
			fmt.Println("  \"" + hermesVenvPython() + "\" -m pip install concurrent-log-handler")
		}
		waitReturn()
		return
	}
	printSuccess("Hermes 可正常运行 ✔")
	fmt.Println(firstLines(out, 6))
	waitReturn()
}

// pickModel 从内置精选清单选模型，或"自定义输入"手输。不联网。
// ESC（菜单或自定义输入处）→ 返回 ""，表示取消、由调用方退回上一级。
func pickModel() string {
	printTip("清单为 DMXAPI 精选常用名；个别型号如已下线，请用\"自定义输入\"填写准确名称。")
	items := make([]menuItem, 0, len(presetModels)+1)
	for _, m := range presetModels {
		items = append(items, menuItem{Label: m.ID, Desc: m.Hint})
	}
	items = append(items, menuItem{Label: "✏️  自定义输入...", Desc: "手动输入其它模型名"})
	idx := selectMenu("选择模型（DMXAPI 常用）：", items)
	if idx < 0 {
		return "" // ESC：取消
	}
	if idx == len(items)-1 {
		m, esc := mustInput("请输入模型名（如 deepseek-v4-flash）")
		if esc {
			return "" // ESC：取消
		}
		printInfo("已选自定义模型：" + m + "（将随本配置保存，并出现在 Hermes 的 /model 列表）")
		return m
	}
	return presetModels[idx].ID
}

// askContextLength 读取上下文窗口 token 数。ESC 取消时返回 (0, true)。
func askContextLength(def string) (int, bool) {
	raw, esc := styledInputDefault("上下文窗口 token 数（回车留空=交 Hermes 自动）", def)
	if esc {
		return 0, true
	}
	v := strings.TrimSpace(raw)
	if v == "" || v == "0" {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		printWarning("无效数字，按自动处理。")
		return 0, false
	}
	return n, false
}

// ── 小工具 ──

// mustInput 必填文本输入：空值重问；ESC 取消时返回 ("", true)。
func mustInput(label string) (string, bool) {
	for {
		v, esc := styledInput(label)
		if esc {
			return "", true
		}
		if v != "" {
			return v, false
		}
		printError("不能为空，请重新输入。")
	}
}

// mustPassword 必填密钥输入：空值重问；ESC 取消时返回 ("", true)。
func mustPassword(label string) (string, bool) {
	for {
		v, esc := readPassword(label)
		if esc {
			return "", true
		}
		if v != "" {
			return v, false
		}
		printError("不能为空，请重新输入。")
	}
}

func hostOf(u string) string {
	s := strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	return s
}

func firstLines(s string, n int) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
