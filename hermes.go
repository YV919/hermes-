package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// hermesBin 解析 hermes 可执行文件：先查 PATH，再退回已知的 venv 安装路径。
func hermesBin() string {
	if p, err := exec.LookPath("hermes"); err == nil {
		return p
	}
	home, _ := os.UserHomeDir()
	for _, c := range []string{
		filepath.Join(home, "AppData", "Local", "hermes", "hermes-agent", "venv", "Scripts", "hermes.exe"),
		filepath.Join(home, "AppData", "Local", "hermes", "hermes-agent", "venv", "Scripts", "hermes"),
		filepath.Join(home, ".local", "share", "hermes", "hermes-agent", "venv", "bin", "hermes"),
	} {
		if fileExists(c) {
			return c
		}
	}
	return "hermes"
}

func runHermes(args ...string) (string, error) {
	out, err := exec.Command(hermesBin(), args...).Output()
	return string(out), err
}

// runHermesCombined 同时捕获 stdout+stderr（自检时需要看 stderr 里的依赖报错）。
func runHermesCombined(args ...string) (string, error) {
	out, err := exec.Command(hermesBin(), args...).CombinedOutput()
	return string(out), err
}

// hermesVenvPython 推断 Hermes venv 里的 python 路径，用于自检时给出修复命令。
func hermesVenvPython() string {
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "windows" {
		return filepath.Join(home, "AppData", "Local", "hermes", "hermes-agent", "venv", "Scripts", "python.exe")
	}
	return filepath.Join(home, ".local", "share", "hermes", "hermes-agent", "venv", "bin", "python")
}

// locateHermesConfig 定位 Hermes 真实配置文件：优先 `hermes config path`，失败则平台默认回退。
func locateHermesConfig() (string, error) {
	if out, err := runHermes("config", "path"); err == nil {
		if p := strings.TrimSpace(out); p != "" && fileExists(p) {
			return p, nil
		}
	}
	home, _ := os.UserHomeDir()
	var candidates []string
	if runtime.GOOS == "windows" {
		if la := os.Getenv("LOCALAPPDATA"); la != "" {
			candidates = append(candidates, filepath.Join(la, "hermes", "config.yaml"))
		}
		candidates = append(candidates, filepath.Join(home, "AppData", "Local", "hermes", "config.yaml"))
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		candidates = append(candidates, filepath.Join(x, "hermes", "config.yaml"))
	}
	candidates = append(candidates,
		filepath.Join(home, ".config", "hermes", "config.yaml"),
		filepath.Join(home, ".hermes", "config.yaml"),
	)
	for _, c := range candidates {
		if fileExists(c) {
			return c, nil
		}
	}
	return "", fmt.Errorf("未找到 Hermes 配置文件——请确认已安装 Hermes，或先运行一次 `hermes` 生成配置")
}

// normalizeBaseURL 规范化 base_url：补 https、去尾斜杠、确保以 /v1 结尾。
func normalizeBaseURL(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return s
	}
	if !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") {
		s = "https://" + s
	}
	s = strings.TrimRight(s, "/")
	if !strings.HasSuffix(s, "/v1") {
		s = s + "/v1"
	}
	return s
}

// providerName 把配置名规范化为 custom_providers 条目名（仅作标识，活动态走内联 custom 不依赖它）。
func providerName(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	n = strings.ReplaceAll(n, " ", "-")
	if n == "" {
		n = "dmxapi"
	}
	return n
}

// ── yaml.Node 辅助：在映射节点上按 key 读/写，保留其余节点不变 ──

func mapGet(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// mapDelete 从映射节点删除指定 key 及其 value，删除成功返回 true。
func mapDelete(m *yaml.Node, key string) bool {
	if m == nil || m.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return true
		}
	}
	return false
}

func mapSetNode(m *yaml.Node, key string, val *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = val
			return
		}
	}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, val)
}

func mapSetScalar(m *yaml.Node, key, value string) {
	if v := mapGet(m, key); v != nil {
		v.Kind = yaml.ScalarNode
		v.Tag = "!!str"
		v.Value = value
		v.Style = 0
		v.Content = nil
		return
	}
	mapSetNode(m, key, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
}

func mapSetInt(m *yaml.Node, key string, value int) {
	s := strconv.Itoa(value)
	if v := mapGet(m, key); v != nil {
		v.Kind = yaml.ScalarNode
		v.Tag = "!!int"
		v.Value = s
		v.Style = 0
		v.Content = nil
		return
	}
	mapSetNode(m, key, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: s})
}

// mapSetBool 写入裸 YAML bool（Tag=!!bool, 小写 true/false, Style=0）。
// 关键：Hermes normalizer（config.py）仅当 isinstance(bool) 才透传 discover_models，
// 写成字符串 "false" 会被丢弃，故必须是裸 bool。
func mapSetBool(m *yaml.Node, key string, value bool) {
	s := "false"
	if value {
		s = "true"
	}
	if v := mapGet(m, key); v != nil {
		v.Kind = yaml.ScalarNode
		v.Tag = "!!bool"
		v.Value = s
		v.Style = 0
		v.Content = nil
		return
	}
	mapSetNode(m, key, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: s})
}

// modelInner 构建 custom_providers[].models[<id>] 的值节点：ctx>0 时带 context_length，否则空映射（auto）。
func modelInner(ctx int) *yaml.Node {
	n := &yaml.Node{Kind: yaml.MappingNode}
	if ctx > 0 {
		mapSetInt(n, "context_length", ctx)
	}
	return n
}

// applyProfileToConfig 把配置 P 写进 Hermes 的 config.yaml：内联活动 model 块 + upsert custom_providers 条目。
// 写前自动备份，返回备份路径。
func applyProfileToConfig(configPath string, p Profile) (backupPath string, err error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return "", err
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return "", fmt.Errorf("解析 Hermes 配置失败: %v", err)
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return "", fmt.Errorf("Hermes 配置格式异常（顶层不是映射）")
	}
	doc := root.Content[0]

	// 命名 provider 标识：model.provider 用 custom:<name> 绑定到下面的 custom_providers 条目，
	// 二者用同一 providerName(p.Name) 保证一致——否则 Hermes 的 /model 会退化成对 base_url 全量发现。
	pname := providerName(p.Name)

	// 1) 内联活动配置 model: 块（bare-custom，Hermes 官方向导范式）
	modelNode := mapGet(doc, "model")
	if modelNode == nil || modelNode.Kind != yaml.MappingNode {
		modelNode = &yaml.Node{Kind: yaml.MappingNode}
		mapSetNode(doc, "model", modelNode)
	}
	mapSetScalar(modelNode, "default", p.Model)
	mapSetScalar(modelNode, "provider", "custom:"+pname)
	mapSetScalar(modelNode, "base_url", p.BaseURL)
	mapSetScalar(modelNode, "api_key", p.APIKey)

	// 2) custom_providers 条目（按 base_url upsert，承载 context_length）
	cpNode := mapGet(doc, "custom_providers")
	if cpNode == nil || cpNode.Kind != yaml.SequenceNode {
		cpNode = &yaml.Node{Kind: yaml.SequenceNode}
		mapSetNode(doc, "custom_providers", cpNode)
	}
	cpNode.Style = 0 // 强制块状渲染（原 `[]` 是 flow 样式）
	var entry *yaml.Node
	for _, e := range cpNode.Content {
		if e.Kind == yaml.MappingNode {
			if bu := mapGet(e, "base_url"); bu != nil && bu.Value == p.BaseURL {
				entry = e
				break
			}
		}
	}
	if entry == nil {
		entry = &yaml.Node{Kind: yaml.MappingNode}
		cpNode.Content = append(cpNode.Content, entry)
	}
	mapSetScalar(entry, "name", pname)
	mapSetScalar(entry, "base_url", p.BaseURL)
	mapSetScalar(entry, "api_key", p.APIKey)
	mapSetScalar(entry, "api_mode", "chat_completions")
	mapSetScalar(entry, "model", p.Model)
	// discover_models: false → Hermes 的 hermes model / /model 只显示下面 models 里的精选，
	// 不再对 base_url 拉取全部模型（751 个）。必须是裸 bool。
	mapSetBool(entry, "discover_models", false)
	// models 列出全部精选模型（各带自己的预设 context_length）+ 当前选中模型。先 preset 后 P.Model，
	// 用 mapSetNode 覆盖去重——P.Model 若已在精选里则覆盖而非重复 key（重复 key 会序列化报错）。
	models := &yaml.Node{Kind: yaml.MappingNode}
	for _, pm := range presetModels {
		models.Content = append(models.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: pm.ID},
			modelInner(pm.ContextLength))
	}
	// 选中模型上下文窗口取值优先级：模型预设值 > 本配置手填值 > 无（auto，自定义模型）。
	// 预设模型固定用自己的预设；只有非预设（自定义）模型才用本配置手填的 context_length。
	selCtx := presetContextLength(p.Model)
	if selCtx == 0 {
		selCtx = p.ContextLength
	}
	mapSetNode(models, p.Model, modelInner(selCtx)) // 覆盖式写入：在精选中则覆盖，不在则追加
	mapSetNode(entry, "models", models)

	// 3) 多模型块跟随：把已启用的委派/辅助块刷新到这套配置的 provider/base_url/api_key
	//    （保留各自模型名）。只动在用的块；无多模型块时为 no-op，不影响其它写入路径。
	syncMultiModelDoc(doc, "custom:"+pname, p.BaseURL, p.APIKey)

	return writeConfigAtomic(configPath, &root)
}

// writeConfigAtomic 把 root 序列化（2 空格缩进，匹配 Hermes 风格），备份原配置后原子写入，返回备份路径。
func writeConfigAtomic(configPath string, root *yaml.Node) (backupPath string, err error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		enc.Close()
		return "", err
	}
	enc.Close()

	// 备份后原子写入
	backupPath, err = backupConfig(configPath)
	if err != nil {
		return "", fmt.Errorf("备份失败（已中止写入，未改动配置）: %v", err)
	}
	tmp := configPath + ".dmxtmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o600); err != nil {
		return backupPath, err
	}
	if err := os.Rename(tmp, configPath); err != nil {
		os.Remove(tmp)
		return backupPath, err
	}
	return backupPath, nil
}

// loadConfigDoc 读取并解析 config.yaml，返回 root 与顶层映射节点 doc。校验顶层是映射。
func loadConfigDoc(configPath string) (root *yaml.Node, doc *yaml.Node, err error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, nil, err
	}
	root = &yaml.Node{}
	if err := yaml.Unmarshal(data, root); err != nil {
		return nil, nil, fmt.Errorf("解析 Hermes 配置失败: %v", err)
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return nil, nil, fmt.Errorf("Hermes 配置格式异常（顶层不是映射）")
	}
	return root, root.Content[0], nil
}

// clearActiveModel 删除 config.yaml 的 model: 块（清除当前生效配置），保留其它字段。备份后原子写入。
func clearActiveModel(configPath string) (backupPath string, err error) {
	root, doc, err := loadConfigDoc(configPath)
	if err != nil {
		return "", err
	}
	mapDelete(doc, "model")
	return writeConfigAtomic(configPath, root)
}

// clearToolConfig 删除 model: 块，并移除 custom_providers 中 base_url ∈ baseURLs 的条目；
// 若 custom_providers 因此清空，则连键一并删除。保留其它字段。备份后原子写入。
func clearToolConfig(configPath string, baseURLs []string) (backupPath string, err error) {
	root, doc, err := loadConfigDoc(configPath)
	if err != nil {
		return "", err
	}
	mapDelete(doc, "model")
	if cp := mapGet(doc, "custom_providers"); cp != nil && cp.Kind == yaml.SequenceNode {
		kept := cp.Content[:0]
		for _, e := range cp.Content {
			drop := false
			if e.Kind == yaml.MappingNode {
				if bu := mapGet(e, "base_url"); bu != nil {
					for _, u := range baseURLs {
						if bu.Value == u {
							drop = true
							break
						}
					}
				}
			}
			if !drop {
				kept = append(kept, e)
			}
		}
		cp.Content = kept
		if len(cp.Content) == 0 {
			mapDelete(doc, "custom_providers")
		}
	}
	// 清除所有配置时，把委派/辅助块一并恢复默认，避免悬空指向被删的 custom provider。
	resetMultiModelDoc(doc)
	return writeConfigAtomic(configPath, root)
}

// backupConfig 把当前 config.yaml 复制到备份目录，返回备份文件路径。
func backupConfig(configPath string) (string, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return "", err
	}
	dir, err := backupDir()
	if err != nil {
		return "", err
	}
	dst := filepath.Join(dir, "config-"+time.Now().Format("20060102-150405")+".yaml")
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		return "", err
	}
	return dst, nil
}

// readActiveModel 读取当前活动的 model 块（用于回显当前状态）。
func readActiveModel(configPath string) (model, provider, baseURL string) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return
	}
	var root yaml.Node
	if yaml.Unmarshal(data, &root) != nil || root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return
	}
	m := mapGet(root.Content[0], "model")
	if m == nil {
		return
	}
	if v := mapGet(m, "default"); v != nil {
		model = v.Value
	}
	if v := mapGet(m, "provider"); v != nil {
		provider = v.Value
	}
	if v := mapGet(m, "base_url"); v != nil {
		baseURL = v.Value
	}
	return
}

// ── 多模型协作：委派 delegation + 辅助 auxiliary ──

// auxiliaryTasks 是 Hermes 的 11 个辅助任务名（顺序与 config.yaml 一致）。
var auxiliaryTasks = []string{
	"vision", "web_extract", "compression", "skills_hub", "approval", "mcp",
	"title_generation", "triage_specifier", "kanban_decomposer", "profile_describer", "curator",
}

// readActiveCreds 读取当前生效 model: 块的 provider/base_url/api_key（委派/辅助复用这套凭据）。
// 三者任一为空表示尚无可用的生效配置。
func readActiveCreds(configPath string) (provider, baseURL, apiKey string) {
	_, doc, err := loadConfigDoc(configPath)
	if err != nil {
		return
	}
	m := mapGet(doc, "model")
	if m == nil {
		return
	}
	if v := mapGet(m, "provider"); v != nil {
		provider = v.Value
	}
	if v := mapGet(m, "base_url"); v != nil {
		baseURL = v.Value
	}
	if v := mapGet(m, "api_key"); v != nil {
		apiKey = v.Value
	}
	return
}

// ensureMap 确保 doc 下 key 是映射节点，返回它（缺则新建）。
func ensureMap(doc *yaml.Node, key string) *yaml.Node {
	n := mapGet(doc, key)
	if n == nil || n.Kind != yaml.MappingNode {
		n = &yaml.Node{Kind: yaml.MappingNode}
		mapSetNode(doc, key, n)
	}
	return n
}

// readDelegationModel 读取当前 delegation.model（用于菜单回显）。
func readDelegationModel(configPath string) string {
	_, doc, err := loadConfigDoc(configPath)
	if err != nil {
		return ""
	}
	if d := mapGet(doc, "delegation"); d != nil {
		if v := mapGet(d, "model"); v != nil {
			return v.Value
		}
	}
	return ""
}

// setDelegationModel 把委派模型写进 delegation 块（provider/base_url/api_key 复用当前生效配置）。
// 只 upsert 这四个字符串字段，max_iterations 等其余字段保持原值。备份后原子写入。
func setDelegationModel(configPath, model, provider, baseURL, apiKey string) (backupPath string, err error) {
	root, doc, err := loadConfigDoc(configPath)
	if err != nil {
		return "", err
	}
	d := ensureMap(doc, "delegation")
	mapSetScalar(d, "model", model)
	mapSetScalar(d, "provider", provider)
	mapSetScalar(d, "base_url", baseURL)
	mapSetScalar(d, "api_key", apiKey)
	return writeConfigAtomic(configPath, root)
}

// clearDelegation 清空委派（恢复"用主模型"）：model/provider/base_url/api_key/api_mode 置空，保留其余字段。
func clearDelegation(configPath string) (backupPath string, err error) {
	root, doc, err := loadConfigDoc(configPath)
	if err != nil {
		return "", err
	}
	d := ensureMap(doc, "delegation")
	for _, k := range []string{"model", "provider", "base_url", "api_key", "api_mode"} {
		mapSetScalar(d, k, "")
	}
	return writeConfigAtomic(configPath, root)
}

// readAuxiliaryModel 读取某辅助任务当前的 provider/model（用于菜单回显）。
func readAuxiliaryModel(configPath, task string) (provider, model string) {
	_, doc, err := loadConfigDoc(configPath)
	if err != nil {
		return
	}
	aux := mapGet(doc, "auxiliary")
	if aux == nil {
		return
	}
	t := mapGet(aux, task)
	if t == nil {
		return
	}
	if v := mapGet(t, "provider"); v != nil {
		provider = v.Value
	}
	if v := mapGet(t, "model"); v != nil {
		model = v.Value
	}
	return
}

// setAuxiliaryModel 给某辅助任务指定模型（provider/base_url/api_key 复用当前生效配置）。
// 只 upsert 这四个字符串字段，timeout/extra_body/download_timeout 等保持原值；缺 task 块则新建最小块（不硬编码 timeout）。
func setAuxiliaryModel(configPath, task, model, provider, baseURL, apiKey string) (backupPath string, err error) {
	root, doc, err := loadConfigDoc(configPath)
	if err != nil {
		return "", err
	}
	aux := ensureMap(doc, "auxiliary")
	t := ensureMap(aux, task)
	mapSetScalar(t, "provider", provider)
	mapSetScalar(t, "model", model)
	mapSetScalar(t, "base_url", baseURL)
	mapSetScalar(t, "api_key", apiKey)
	return writeConfigAtomic(configPath, root)
}

// clearAuxiliaryModel 把某辅助任务恢复为用主模型：provider=auto、model/base_url/api_key 置空。
func clearAuxiliaryModel(configPath, task string) (backupPath string, err error) {
	root, doc, err := loadConfigDoc(configPath)
	if err != nil {
		return "", err
	}
	aux := ensureMap(doc, "auxiliary")
	t := ensureMap(aux, task)
	mapSetScalar(t, "provider", "auto")
	for _, k := range []string{"model", "base_url", "api_key"} {
		mapSetScalar(t, k, "")
	}
	return writeConfigAtomic(configPath, root)
}

// ── 多模型块「跟随生效配置 / 整体重置」（切换、清除所有时调用）──

// delegationInUse 报告 delegation 块是否已启用（model 非空）。
func delegationInUse(d *yaml.Node) bool {
	if d == nil || d.Kind != yaml.MappingNode {
		return false
	}
	m := mapGet(d, "model")
	return m != nil && m.Value != ""
}

// auxTaskInUse 报告某 auxiliary 任务块是否已启用（provider 非空且≠auto 且 model 非空，与 auxiliaryFlow 回显一致）。
func auxTaskInUse(t *yaml.Node) bool {
	if t == nil || t.Kind != yaml.MappingNode {
		return false
	}
	prov, model := "", ""
	if v := mapGet(t, "provider"); v != nil {
		prov = v.Value
	}
	if v := mapGet(t, "model"); v != nil {
		model = v.Value
	}
	return prov != "" && prov != "auto" && model != ""
}

// syncMultiModelDoc 把「已启用」的委派/辅助块的 provider/base_url/api_key 刷新为传入凭据，
// 保留各自的 model 及其余字段。不存在或 auto/未启用的块一律不动（无多模型块时为 no-op）。
// 用于切换/应用配置时让多模型块跟随新生效配置——只写字符串字段，遵守类型铁律。返回刷新的块数。
func syncMultiModelDoc(doc *yaml.Node, provider, baseURL, apiKey string) (n int) {
	refresh := func(blk *yaml.Node) {
		mapSetScalar(blk, "provider", provider)
		mapSetScalar(blk, "base_url", baseURL)
		mapSetScalar(blk, "api_key", apiKey)
	}
	if d := mapGet(doc, "delegation"); delegationInUse(d) {
		refresh(d)
		n++
	}
	if aux := mapGet(doc, "auxiliary"); aux != nil && aux.Kind == yaml.MappingNode {
		for _, task := range auxiliaryTasks {
			if t := mapGet(aux, task); auxTaskInUse(t) {
				refresh(t)
				n++
			}
		}
	}
	return n
}

// resetMultiModelDoc 把已存在的委派/辅助块恢复默认：委派字符串字段置空、辅助置 provider:auto+其余空。
// 只动已存在的块、只写字符串字段，不存在的块不新建。用于清除所有配置时一并重置，消除悬空引用。
func resetMultiModelDoc(doc *yaml.Node) {
	if d := mapGet(doc, "delegation"); d != nil && d.Kind == yaml.MappingNode {
		for _, k := range []string{"model", "provider", "base_url", "api_key", "api_mode"} {
			mapSetScalar(d, k, "")
		}
	}
	if aux := mapGet(doc, "auxiliary"); aux != nil && aux.Kind == yaml.MappingNode {
		for _, task := range auxiliaryTasks {
			t := mapGet(aux, task)
			if t == nil || t.Kind != yaml.MappingNode {
				continue
			}
			mapSetScalar(t, "provider", "auto")
			for _, k := range []string{"model", "base_url", "api_key"} {
				mapSetScalar(t, k, "")
			}
		}
	}
}

// resetMultiModelConfig 一次性把委派与全部辅助任务恢复默认（单次备份+原子写）。
// 等价于"清空委派 + 每个辅助任务回 auto"，但只写一次（取代逐个 clearAuxiliaryModel 的多次备份）。
func resetMultiModelConfig(configPath string) (backupPath string, err error) {
	root, doc, err := loadConfigDoc(configPath)
	if err != nil {
		return "", err
	}
	resetMultiModelDoc(doc)
	return writeConfigAtomic(configPath, root)
}

// multiModelInUse 报告当前配置是否有「已启用」的委派或辅助块（切换时据此决定是否提示已同步）。
func multiModelInUse(configPath string) bool {
	_, doc, err := loadConfigDoc(configPath)
	if err != nil {
		return false
	}
	if delegationInUse(mapGet(doc, "delegation")) {
		return true
	}
	if aux := mapGet(doc, "auxiliary"); aux != nil && aux.Kind == yaml.MappingNode {
		for _, task := range auxiliaryTasks {
			if auxTaskInUse(mapGet(aux, task)) {
				return true
			}
		}
	}
	return false
}

// ── API 校验（OpenAI 兼容）──

func httpClient() *http.Client { return &http.Client{Timeout: 30 * time.Second} }
// validateChat 用真实模型发一条最小请求做端到端冒烟。区分鉴权失败 vs 模型名问题。
func validateChat(baseURL, apiKey, model string) error {
	payload := map[string]interface{}{
		"model":      model,
		"messages":   []map[string]string{{"role": "user", "content": "ping"}},
		"max_tokens": 1,
	}
	b, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", baseURL+"/chat/completions", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("无法连接：%v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	switch {
	case resp.StatusCode == 200:
		return nil
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		return fmt.Errorf("鉴权失败 (HTTP %d)：密钥无效或无权限", resp.StatusCode)
	case resp.StatusCode == 404 || resp.StatusCode == 400:
		return fmt.Errorf("模型可能不存在或名称有误 (HTTP %d)：%s", resp.StatusCode, truncate(string(body), 160))
	default:
		return fmt.Errorf("HTTP %d：%s", resp.StatusCode, truncate(string(body), 160))
	}
}
