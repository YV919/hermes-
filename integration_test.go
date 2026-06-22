//go:build integration

package main

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func topKeys(m *yaml.Node) map[string]bool {
	keys := map[string]bool{}
	if m == nil || m.Kind != yaml.MappingNode {
		return keys
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		keys[m.Content[i].Value] = true
	}
	return keys
}

// TestRealConfigRoundTrip 对真实 Hermes 配置的临时副本做落地，验证输出合法 + 顶层设置全保留。
// 仅在能定位到真实配置时运行（CI 上自动跳过）；绝不写真实配置文件。
func TestRealConfigRoundTrip(t *testing.T) {
	real, err := locateHermesConfig()
	if err != nil {
		t.Skip("未找到真实 Hermes 配置: " + err.Error())
	}
	data, err := os.ReadFile(real)
	if err != nil {
		t.Skip(err.Error())
	}
	var before yaml.Node
	if err := yaml.Unmarshal(data, &before); err != nil {
		t.Fatalf("真实配置本身不是合法 YAML?: %v", err)
	}
	beforeKeys := topKeys(before.Content[0])
	t.Logf("真实配置顶层 key 数: %d", len(beforeKeys))

	home := isolateHome(t)
	tmpCfg := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(tmpCfg, data, 0o600); err != nil {
		t.Fatal(err)
	}

	p := Profile{Name: "verify-flash", BaseURL: "https://www.dmxapi.cn/v1", APIKey: "sk-verify", Model: "deepseek-v4-flash", ContextLength: 131072}
	if _, err := applyProfileToConfig(tmpCfg, p); err != nil {
		t.Fatal(err)
	}

	out, err := os.ReadFile(tmpCfg)
	if err != nil {
		t.Fatal(err)
	}
	var after yaml.Node
	if err := yaml.Unmarshal(out, &after); err != nil {
		t.Fatalf("落地后的输出不是合法 YAML: %v", err)
	}
	afterKeys := topKeys(after.Content[0])
	for k := range beforeKeys {
		if !afterKeys[k] {
			t.Errorf("顶层设置丢失: %s", k)
		}
	}
	// 校验活动 model 块
	m := mapGet(after.Content[0], "model")
	wantProvider := "custom:" + providerName(p.Name)
	if v := mapGet(m, "provider"); v == nil || v.Value != wantProvider {
		t.Errorf("model.provider 应为 %s", wantProvider)
	}
	if v := mapGet(m, "default"); v == nil || v.Value != p.Model {
		t.Errorf("model.default 应为 %s", p.Model)
	}

	// 写到稳定路径，供外部用 Hermes 的 PyYAML 再验证一次
	verifyPath := filepath.Join(os.TempDir(), "dmxapi-hermes-verify.yaml")
	if err := os.WriteFile(verifyPath, out, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("保留全部 %d 个顶层 key；验证文件写入 %s", len(beforeKeys), verifyPath)
}
