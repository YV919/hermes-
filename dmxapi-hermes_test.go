package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestNormalizeBaseURL(t *testing.T) {
	cases := map[string]string{
		"https://www.dmxapi.cn/v1":  "https://www.dmxapi.cn/v1",
		"https://www.dmxapi.cn/v1/": "https://www.dmxapi.cn/v1",
		"https://www.dmxapi.cn":     "https://www.dmxapi.cn/v1",
		"www.dmxapi.cn":             "https://www.dmxapi.cn/v1",
		"  https://x.com/v1  ":      "https://x.com/v1",
	}
	for in, want := range cases {
		if got := normalizeBaseURL(in); got != want {
			t.Errorf("normalizeBaseURL(%q)=%q want %q", in, got, want)
		}
	}
}

func TestSanitizeName(t *testing.T) {
	if got := sanitizeName(`a/b:c*?`); strings.ContainsAny(got, `/\:*?"<>|`) {
		t.Errorf("sanitizeName 泄漏非法字符: %q", got)
	}
	if got := sanitizeName("../../etc"); strings.Contains(got, "..") {
		t.Errorf("sanitizeName 未消除 ..: %q", got)
	}
	if got := sanitizeName("CON"); got == "CON" {
		t.Errorf("sanitizeName 未防护保留名: %q", got)
	}
	if got := sanitizeName("   "); got != "config" {
		t.Errorf("空名应回退为 config, got %q", got)
	}
}

func TestProviderName(t *testing.T) {
	if got := providerName("Flash 日常"); got != "flash-日常" {
		t.Errorf("providerName 规范化错误: %q", got)
	}
	if got := providerName(""); got != "dmxapi" {
		t.Errorf("空名应为 dmxapi, got %q", got)
	}
}

func isolateHome(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("HOME", tmp)
	return tmp
}

func TestApplyProfileToConfig(t *testing.T) {
	tmp := isolateHome(t)
	orig := `model:
  default: gpt-5.5
  provider: nous
custom_providers: []
display:
  personality: kawaii
# 末尾注释
`
	cfgPath := filepath.Join(tmp, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}

	p := Profile{Name: "Flash 日常", BaseURL: "https://www.dmxapi.cn/v1", APIKey: "sk-test123", Model: "deepseek-v4-flash", ContextLength: 131072}
	backup, err := applyProfileToConfig(cfgPath, p)
	if err != nil {
		t.Fatal(err)
	}
	if !fileExists(backup) {
		t.Errorf("未生成备份: %s", backup)
	}

	data, _ := os.ReadFile(cfgPath)
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		t.Fatalf("结果不是合法 YAML: %v", err)
	}
	doc := root.Content[0]

	m := mapGet(doc, "model")
	wantProvider := "custom:" + providerName(p.Name)
	if v := mapGet(m, "provider"); v == nil || v.Value != wantProvider {
		t.Errorf("model.provider 应为 %s, got %v", wantProvider, v)
	}
	if v := mapGet(m, "default"); v == nil || v.Value != "deepseek-v4-flash" {
		t.Errorf("model.default 错误: %v", v)
	}
	if v := mapGet(m, "base_url"); v == nil || v.Value != p.BaseURL {
		t.Errorf("model.base_url 错误: %v", v)
	}
	if v := mapGet(m, "api_key"); v == nil || v.Value != p.APIKey {
		t.Errorf("model.api_key 错误: %v", v)
	}

	// 不相关设置必须保留
	disp := mapGet(doc, "display")
	if disp == nil || mapGet(disp, "personality") == nil {
		t.Errorf("无关的 display 设置丢失了")
	}

	cp := mapGet(doc, "custom_providers")
	if cp == nil || cp.Kind != yaml.SequenceNode || len(cp.Content) != 1 {
		t.Fatalf("custom_providers 未正确 upsert: %+v", cp)
	}
	entry := cp.Content[0]
	if v := mapGet(entry, "base_url"); v == nil || v.Value != p.BaseURL {
		t.Errorf("entry.base_url 错误")
	}
	if v := mapGet(entry, "api_mode"); v == nil || v.Value != "chat_completions" {
		t.Errorf("entry.api_mode 错误")
	}
	// discover_models 必须是裸 bool false（否则 Hermes normalizer 丢弃）
	dm := mapGet(entry, "discover_models")
	if dm == nil {
		t.Errorf("discover_models 缺失")
	} else if dm.Tag != "!!bool" || dm.Value != "false" {
		t.Errorf("discover_models 应为 bool false, got tag=%q val=%q", dm.Tag, dm.Value)
	}
	modelsNode := mapGet(entry, "models")
	inner := mapGet(modelsNode, p.Model)
	if inner == nil {
		t.Fatalf("models[%s] 缺失", p.Model)
	}
	// deepseek-v4-flash 是预设模型 → 预设值 1000000 胜过手填的 131072
	if cl := mapGet(inner, "context_length"); cl == nil || cl.Value != "1000000" {
		t.Errorf("预设模型 context_length 应为预设值 1000000, got %v", cl)
	}
	// 8 个精选 key 全在
	for _, pm := range presetModels {
		if mapGet(modelsNode, pm.ID) == nil {
			t.Errorf("精选模型 %s 未写入 models", pm.ID)
		}
	}
	// p.Model=deepseek-v4-flash 在精选里 → 去重，models 恰为 8 个 key（Content 长度 16）
	if got := len(modelsNode.Content) / 2; got != len(presetModels) {
		t.Errorf("models key 数应为 %d（去重），实际 %d", len(presetModels), got)
	}

	model, provider, baseURL := readActiveModel(cfgPath)
	if model != p.Model || provider != wantProvider || baseURL != p.BaseURL {
		t.Errorf("readActiveModel 得到 (%s,%s,%s)", model, provider, baseURL)
	}
}

func TestApplyUpsertDedupByBaseURL(t *testing.T) {
	tmp := isolateHome(t)
	orig := `model:
  default: x
  provider: custom
custom_providers:
- name: DMXAPI
  base_url: https://www.dmxapi.cn/v1
  api_key: sk-BROKENsk-BROKEN
  model: old
`
	cfgPath := filepath.Join(tmp, "config.yaml")
	os.WriteFile(cfgPath, []byte(orig), 0o600)

	p := Profile{Name: "n", BaseURL: "https://www.dmxapi.cn/v1", APIKey: "sk-new", Model: "deepseek-v4-flash"}
	if _, err := applyProfileToConfig(cfgPath, p); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(cfgPath)
	var root yaml.Node
	yaml.Unmarshal(data, &root)
	cp := mapGet(root.Content[0], "custom_providers")
	if cp == nil || len(cp.Content) != 1 {
		t.Fatalf("应按 base_url 去重为 1 条, got %d", len(cp.Content))
	}
	if v := mapGet(cp.Content[0], "api_key"); v == nil || v.Value != "sk-new" {
		t.Errorf("损坏的 api_key 未被覆盖修复: %v", v)
	}
}

func TestContextLengthOmittedWhenZero(t *testing.T) {
	tmp := isolateHome(t)
	cfgPath := filepath.Join(tmp, "config.yaml")
	os.WriteFile(cfgPath, []byte("model:\n  default: x\n  provider: custom\n"), 0o600)
	p := Profile{Name: "n", BaseURL: "https://www.dmxapi.cn/v1", APIKey: "sk-a", Model: "m", ContextLength: 0}
	if _, err := applyProfileToConfig(cfgPath, p); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(cfgPath)
	var root yaml.Node
	yaml.Unmarshal(data, &root)
	entry := mapGet(root.Content[0], "custom_providers").Content[0]
	inner := mapGet(mapGet(entry, "models"), "m")
	if inner == nil {
		t.Fatal("models[m] 缺失")
	}
	if cl := mapGet(inner, "context_length"); cl != nil {
		t.Errorf("ContextLength=0 时不应写 context_length, got %v", cl.Value)
	}
}

// 预设模型应把各自的 context_length 写进 models（未选中的也带），选中且未手填则用预设值。
func TestPresetContextLengthWritten(t *testing.T) {
	tmp := isolateHome(t)
	cfgPath := filepath.Join(tmp, "config.yaml")
	os.WriteFile(cfgPath, []byte("model:\n  default: x\n  provider: custom\n"), 0o600)
	// 选中 kimi-k2.7-code、不手填 context（ContextLength=0）→ 应取预设 256000
	p := Profile{Name: "n", BaseURL: "https://www.dmxapi.cn/v1", APIKey: "sk-a", Model: "kimi-k2.7-code", ContextLength: 0}
	if _, err := applyProfileToConfig(cfgPath, p); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(cfgPath)
	var root yaml.Node
	yaml.Unmarshal(data, &root)
	models := mapGet(mapGet(root.Content[0], "custom_providers").Content[0], "models")

	check := func(model string, want string) {
		inner := mapGet(models, model)
		if inner == nil {
			t.Fatalf("models[%s] 缺失", model)
		}
		cl := mapGet(inner, "context_length")
		if cl == nil || cl.Value != want {
			t.Errorf("models[%s].context_length=%v, want %s", model, cl, want)
		}
	}
	// 选中的预设模型用预设值
	check("kimi-k2.7-code", "256000")
	// 未选中的预设模型也各带自己的预设值
	check("gpt-5.5", "273000")
	check("claude-opus-4-8-cc", "1000000")
}

// 自定义（非预设）模型：无预设 → 用本配置手填的 context_length。
func TestCustomModelContextOverride(t *testing.T) {
	tmp := isolateHome(t)
	cfgPath := filepath.Join(tmp, "config.yaml")
	os.WriteFile(cfgPath, []byte("model:\n  default: x\n  provider: custom\n"), 0o600)
	p := Profile{Name: "n", BaseURL: "https://www.dmxapi.cn/v1", APIKey: "sk-a", Model: "my-custom-x", ContextLength: 131072}
	if _, err := applyProfileToConfig(cfgPath, p); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(cfgPath)
	var root yaml.Node
	yaml.Unmarshal(data, &root)
	models := mapGet(mapGet(root.Content[0], "custom_providers").Content[0], "models")
	inner := mapGet(models, "my-custom-x")
	if inner == nil {
		t.Fatal("models[my-custom-x] 缺失")
	}
	if cl := mapGet(inner, "context_length"); cl == nil || cl.Value != "131072" {
		t.Errorf("自定义模型应写手填的 131072, got %v", cl)
	}
}

func TestSaveLoadListDeleteProfile(t *testing.T) {
	isolateHome(t)
	p := Profile{Name: "test1", BaseURL: "https://www.dmxapi.cn/v1", APIKey: "sk-x", Model: "m", ContextLength: 1000}
	if err := saveProfile(p); err != nil {
		t.Fatal(err)
	}
	got, err := loadProfile("test1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "m" || got.ContextLength != 1000 || got.AppVersion != appVersion {
		t.Errorf("读回的配置不符: %+v", got)
	}
	if list, _ := listProfiles(); len(list) != 1 || list[0].Name != "test1" {
		t.Errorf("listProfiles 错误: %+v", list)
	}
	if err := deleteProfile("test1"); err != nil {
		t.Fatal(err)
	}
	if list, _ := listProfiles(); len(list) != 0 {
		t.Errorf("配置未删除")
	}
}

func TestDeleteAllProfiles(t *testing.T) {
	isolateHome(t)
	for _, n := range []string{"a", "b", "c"} {
		if err := saveProfile(Profile{Name: n, BaseURL: "https://www.dmxapi.cn/v1", APIKey: "sk-x", Model: "m"}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := deleteAllProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if got != 3 {
		t.Errorf("应删除 3 套, got %d", got)
	}
	if list, _ := listProfiles(); len(list) != 0 {
		t.Errorf("仍残留配置: %+v", list)
	}
}

func TestClearActiveModel(t *testing.T) {
	tmp := isolateHome(t)
	orig := `model:
  default: deepseek-v4-flash
  provider: custom
  base_url: https://www.dmxapi.cn/v1
display:
  personality: kawaii
`
	cfgPath := filepath.Join(tmp, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	backup, err := clearActiveModel(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !fileExists(backup) {
		t.Errorf("未生成备份: %s", backup)
	}
	data, _ := os.ReadFile(cfgPath)
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		t.Fatalf("结果不是合法 YAML: %v", err)
	}
	doc := root.Content[0]
	if mapGet(doc, "model") != nil {
		t.Errorf("model 块未被清除")
	}
	disp := mapGet(doc, "display")
	if disp == nil || mapGet(disp, "personality") == nil {
		t.Errorf("无关的 display 设置丢失了")
	}
}

func TestClearToolConfig(t *testing.T) {
	tmp := isolateHome(t)
	orig := `model:
  default: deepseek-v4-flash
  provider: custom
custom_providers:
- name: dmxapi
  base_url: https://www.dmxapi.cn/v1
  api_key: sk-a
- name: other
  base_url: https://other.example.com/v1
  api_key: sk-b
display:
  personality: kawaii
`
	cfgPath := filepath.Join(tmp, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := clearToolConfig(cfgPath, []string{"https://www.dmxapi.cn/v1"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(cfgPath)
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		t.Fatalf("结果不是合法 YAML: %v", err)
	}
	doc := root.Content[0]
	if mapGet(doc, "model") != nil {
		t.Errorf("model 块未被清除")
	}
	cp := mapGet(doc, "custom_providers")
	if cp == nil || cp.Kind != yaml.SequenceNode || len(cp.Content) != 1 {
		t.Fatalf("custom_providers 应剩 1 条未命中条目, got %+v", cp)
	}
	if bu := mapGet(cp.Content[0], "base_url"); bu == nil || bu.Value != "https://other.example.com/v1" {
		t.Errorf("保留的应是未命中条目, got %v", bu)
	}
	if mapGet(doc, "display") == nil {
		t.Errorf("无关的 display 设置丢失了")
	}
}

// 多模型协作：委派模型写入正确，且 delegation 其它字段（max_iterations 等）保留。
func TestSetDelegationModel(t *testing.T) {
	tmp := isolateHome(t)
	orig := `model:
  default: gpt-5.5
  provider: custom:dmxapi
delegation:
  model: ''
  provider: ''
  base_url: ''
  api_key: ''
  max_iterations: 50
  orchestrator_enabled: true
display:
  personality: kawaii
`
	cfgPath := filepath.Join(tmp, "config.yaml")
	os.WriteFile(cfgPath, []byte(orig), 0o600)
	if _, err := setDelegationModel(cfgPath, "deepseek-v4-flash", "custom:dmxapi", "https://www.dmxapi.cn/v1", "sk-x"); err != nil {
		t.Fatal(err)
	}
	_, doc, err := loadConfigDoc(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	d := mapGet(doc, "delegation")
	if v := mapGet(d, "model"); v == nil || v.Value != "deepseek-v4-flash" {
		t.Errorf("delegation.model 错误: %v", v)
	}
	if v := mapGet(d, "provider"); v == nil || v.Value != "custom:dmxapi" {
		t.Errorf("delegation.provider 错误: %v", v)
	}
	if v := mapGet(d, "base_url"); v == nil || v.Value != "https://www.dmxapi.cn/v1" {
		t.Errorf("delegation.base_url 错误: %v", v)
	}
	if v := mapGet(d, "api_key"); v == nil || v.Value != "sk-x" {
		t.Errorf("delegation.api_key 错误: %v", v)
	}
	// 未触碰的字段必须保留（含类型）
	if v := mapGet(d, "max_iterations"); v == nil || v.Value != "50" || v.Tag != "!!int" {
		t.Errorf("max_iterations 应保留为 int 50, got %v", v)
	}
	if v := mapGet(d, "orchestrator_enabled"); v == nil || v.Value != "true" || v.Tag != "!!bool" {
		t.Errorf("orchestrator_enabled 应保留为 bool true, got %v", v)
	}
	if mapGet(doc, "display") == nil {
		t.Errorf("无关的 display 设置丢失了")
	}

	// clearDelegation 应清空字符串字段、保留 max_iterations
	if _, err := clearDelegation(cfgPath); err != nil {
		t.Fatal(err)
	}
	_, doc2, _ := loadConfigDoc(cfgPath)
	d2 := mapGet(doc2, "delegation")
	if v := mapGet(d2, "model"); v == nil || v.Value != "" {
		t.Errorf("clearDelegation 后 model 应为空, got %v", v)
	}
	if v := mapGet(d2, "max_iterations"); v == nil || v.Value != "50" {
		t.Errorf("clearDelegation 不应动 max_iterations, got %v", v)
	}
}

// 多模型协作：辅助任务写入正确、timeout 保留；clearAuxiliaryModel 恢复 auto。
func TestSetAuxiliaryModel(t *testing.T) {
	tmp := isolateHome(t)
	orig := `model:
  default: gpt-5.5
  provider: custom:dmxapi
auxiliary:
  title_generation:
    provider: auto
    model: ''
    base_url: ''
    api_key: ''
    timeout: 30
    extra_body: {}
`
	cfgPath := filepath.Join(tmp, "config.yaml")
	os.WriteFile(cfgPath, []byte(orig), 0o600)
	if _, err := setAuxiliaryModel(cfgPath, "title_generation", "deepseek-v4-flash", "custom:dmxapi", "https://www.dmxapi.cn/v1", "sk-x"); err != nil {
		t.Fatal(err)
	}
	_, doc, _ := loadConfigDoc(cfgPath)
	tg := mapGet(mapGet(doc, "auxiliary"), "title_generation")
	if v := mapGet(tg, "model"); v == nil || v.Value != "deepseek-v4-flash" {
		t.Errorf("auxiliary.title_generation.model 错误: %v", v)
	}
	if v := mapGet(tg, "provider"); v == nil || v.Value != "custom:dmxapi" {
		t.Errorf("auxiliary.title_generation.provider 错误: %v", v)
	}
	if v := mapGet(tg, "base_url"); v == nil || v.Value != "https://www.dmxapi.cn/v1" {
		t.Errorf("auxiliary.title_generation.base_url 错误: %v", v)
	}
	// timeout 必须保留为 int 30
	if v := mapGet(tg, "timeout"); v == nil || v.Value != "30" || v.Tag != "!!int" {
		t.Errorf("timeout 应保留为 int 30, got %v", v)
	}

	// clearAuxiliaryModel：provider=auto、model=''
	if _, err := clearAuxiliaryModel(cfgPath, "title_generation"); err != nil {
		t.Fatal(err)
	}
	_, doc2, _ := loadConfigDoc(cfgPath)
	tg2 := mapGet(mapGet(doc2, "auxiliary"), "title_generation")
	if v := mapGet(tg2, "provider"); v == nil || v.Value != "auto" {
		t.Errorf("clearAuxiliaryModel 后 provider 应为 auto, got %v", v)
	}
	if v := mapGet(tg2, "model"); v == nil || v.Value != "" {
		t.Errorf("clearAuxiliaryModel 后 model 应为空, got %v", v)
	}
	if v := mapGet(tg2, "timeout"); v == nil || v.Value != "30" {
		t.Errorf("clearAuxiliaryModel 不应动 timeout, got %v", v)
	}
}

// readActiveCreds 从 model 块读出 provider/base_url/api_key。
func TestReadActiveCreds(t *testing.T) {
	tmp := isolateHome(t)
	cfgPath := filepath.Join(tmp, "config.yaml")
	os.WriteFile(cfgPath, []byte("model:\n  provider: custom:dmxapi\n  base_url: https://www.dmxapi.cn/v1\n  api_key: sk-z\n"), 0o600)
	prov, bu, key := readActiveCreds(cfgPath)
	if prov != "custom:dmxapi" || bu != "https://www.dmxapi.cn/v1" || key != "sk-z" {
		t.Errorf("readActiveCreds 得到 (%s,%s,%s)", prov, bu, key)
	}
}

// setAuxiliaryModel 在 auxiliary / 该 task 块都不存在时应自动新建（不硬编码 timeout）。
func TestSetAuxiliaryModelCreatesMissingBlock(t *testing.T) {
	tmp := isolateHome(t)
	cfgPath := filepath.Join(tmp, "config.yaml")
	// 配置里完全没有 auxiliary 块
	os.WriteFile(cfgPath, []byte("model:\n  provider: custom:dmxapi\n  base_url: https://www.dmxapi.cn/v1\n  api_key: sk-z\n"), 0o600)
	if _, err := setAuxiliaryModel(cfgPath, "vision", "deepseek-v4-flash", "custom:dmxapi", "https://www.dmxapi.cn/v1", "sk-z"); err != nil {
		t.Fatal(err)
	}
	_, doc, _ := loadConfigDoc(cfgPath)
	aux := mapGet(doc, "auxiliary")
	if aux == nil {
		t.Fatal("auxiliary 块未被新建")
	}
	v := mapGet(aux, "vision")
	if v == nil {
		t.Fatal("auxiliary.vision 块未被新建")
	}
	if m := mapGet(v, "model"); m == nil || m.Value != "deepseek-v4-flash" {
		t.Errorf("新建块 model 错误: %v", m)
	}
	// 不应硬编码 timeout
	if to := mapGet(v, "timeout"); to != nil {
		t.Errorf("新建块不应写 timeout, got %v", to.Value)
	}
}
