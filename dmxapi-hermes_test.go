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
	if v := mapGet(m, "provider"); v == nil || v.Value != "custom" {
		t.Errorf("model.provider 应为 custom, got %v", v)
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
	if cl := mapGet(inner, "context_length"); cl == nil || cl.Value != "131072" {
		t.Errorf("context_length 错误: %v", cl)
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
	if model != p.Model || provider != "custom" || baseURL != p.BaseURL {
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
