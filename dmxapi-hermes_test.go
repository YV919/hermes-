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

// 切换/应用配置时，已启用的委派/辅助块应跟随新配置刷新 provider/base_url/api_key（保留模型名）；
// auto/未启用的块不动，从不存在的块也不会被新建。
func TestApplyRefreshesMultiModelCreds(t *testing.T) {
	tmp := isolateHome(t)
	// 配置 A：model + 已启用的委派 + 一个已启用辅助(title_generation) + 一个 auto 辅助(vision)
	orig := `model:
  default: gpt-5.5
  provider: custom:cfga
  base_url: https://a.example.com/v1
  api_key: sk-aaa
delegation:
  model: deepseek-v4-flash
  provider: custom:cfga
  base_url: https://a.example.com/v1
  api_key: sk-aaa
  max_iterations: 50
auxiliary:
  title_generation:
    provider: custom:cfga
    model: kimi-k2.7-code
    base_url: https://a.example.com/v1
    api_key: sk-aaa
    timeout: 30
  vision:
    provider: auto
    model: ''
    base_url: ''
    api_key: ''
`
	cfgPath := filepath.Join(tmp, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}

	// 切到配置 B（不同地址/密钥/名字）
	p := Profile{Name: "ProfB", BaseURL: "https://b.example.com/v1", APIKey: "sk-bbb", Model: "deepseek-v4-pro"}
	if _, err := applyProfileToConfig(cfgPath, p); err != nil {
		t.Fatal(err)
	}
	wantProv := "custom:" + providerName(p.Name)

	_, doc, err := loadConfigDoc(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	// 委派：凭据刷新到 B、模型名保留、max_iterations(int) 保留
	d := mapGet(doc, "delegation")
	if v := mapGet(d, "provider"); v == nil || v.Value != wantProv {
		t.Errorf("delegation.provider 应刷新为 %s, got %v", wantProv, v)
	}
	if v := mapGet(d, "base_url"); v == nil || v.Value != p.BaseURL {
		t.Errorf("delegation.base_url 应刷新为 B, got %v", v)
	}
	if v := mapGet(d, "api_key"); v == nil || v.Value != p.APIKey {
		t.Errorf("delegation.api_key 应刷新为 B, got %v", v)
	}
	if v := mapGet(d, "model"); v == nil || v.Value != "deepseek-v4-flash" {
		t.Errorf("delegation.model 应保留, got %v", v)
	}
	if v := mapGet(d, "max_iterations"); v == nil || v.Value != "50" || v.Tag != "!!int" {
		t.Errorf("delegation.max_iterations 应原样保留为 int 50, got %v", v)
	}

	// 已启用辅助：凭据刷新、模型名与 timeout 保留
	tg := mapGet(mapGet(doc, "auxiliary"), "title_generation")
	if v := mapGet(tg, "base_url"); v == nil || v.Value != p.BaseURL {
		t.Errorf("auxiliary.title_generation.base_url 应刷新为 B, got %v", v)
	}
	if v := mapGet(tg, "api_key"); v == nil || v.Value != p.APIKey {
		t.Errorf("auxiliary.title_generation.api_key 应刷新为 B, got %v", v)
	}
	if v := mapGet(tg, "provider"); v == nil || v.Value != wantProv {
		t.Errorf("auxiliary.title_generation.provider 应刷新, got %v", v)
	}
	if v := mapGet(tg, "model"); v == nil || v.Value != "kimi-k2.7-code" {
		t.Errorf("auxiliary.title_generation.model 应保留, got %v", v)
	}
	if v := mapGet(tg, "timeout"); v == nil || v.Value != "30" || v.Tag != "!!int" {
		t.Errorf("auxiliary.title_generation.timeout 应原样保留, got %v", v)
	}

	// auto 辅助：不应被刷新（base_url 仍为空）
	vis := mapGet(mapGet(doc, "auxiliary"), "vision")
	if v := mapGet(vis, "provider"); v == nil || v.Value != "auto" {
		t.Errorf("auxiliary.vision 应仍为 auto, got %v", v)
	}
	if v := mapGet(vis, "base_url"); v == nil || v.Value != "" {
		t.Errorf("auto 的 vision.base_url 不应被写入, got %v", v)
	}

	// 从不存在的辅助任务不应被新建
	if mapGet(mapGet(doc, "auxiliary"), "curator") != nil {
		t.Errorf("未配置的 auxiliary.curator 不应被新建")
	}
}

// 清除所有配置时，custom_providers 条目被删的同时，委派/辅助块也应一并恢复默认（消除悬空引用）。
func TestClearToolConfigResetsMultiModel(t *testing.T) {
	tmp := isolateHome(t)
	orig := `model:
  default: gpt-5.5
  provider: custom:cfgx
  base_url: https://x.example.com/v1
  api_key: sk-xxx
custom_providers:
- name: cfgx
  base_url: https://x.example.com/v1
  api_key: sk-xxx
delegation:
  model: deepseek-v4-flash
  provider: custom:cfgx
  base_url: https://x.example.com/v1
  api_key: sk-xxx
  max_iterations: 50
auxiliary:
  title_generation:
    provider: custom:cfgx
    model: kimi-k2.7-code
    base_url: https://x.example.com/v1
    api_key: sk-xxx
`
	cfgPath := filepath.Join(tmp, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := clearToolConfig(cfgPath, []string{"https://x.example.com/v1"}); err != nil {
		t.Fatal(err)
	}
	_, doc, err := loadConfigDoc(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	// model 块与 custom:cfgx 条目都应没了（否则委派指向它就是悬空）
	if mapGet(doc, "model") != nil {
		t.Errorf("model 块应被清除")
	}
	if mapGet(doc, "custom_providers") != nil {
		t.Errorf("唯一的 custom_providers 条目被删后该键应一并移除")
	}

	// 委派与辅助应已恢复默认，不再指向被删的 custom:cfgx
	d := mapGet(doc, "delegation")
	if v := mapGet(d, "model"); v == nil || v.Value != "" {
		t.Errorf("clearToolConfig 后 delegation.model 应清空, got %v", v)
	}
	if v := mapGet(d, "provider"); v == nil || v.Value != "" {
		t.Errorf("clearToolConfig 后 delegation.provider 应清空, got %v", v)
	}
	tg := mapGet(mapGet(doc, "auxiliary"), "title_generation")
	if v := mapGet(tg, "provider"); v == nil || v.Value != "auto" {
		t.Errorf("clearToolConfig 后 auxiliary.title_generation 应回 auto, got %v", v)
	}
	if v := mapGet(tg, "model"); v == nil || v.Value != "" {
		t.Errorf("clearToolConfig 后 auxiliary.title_generation.model 应清空, got %v", v)
	}
}
