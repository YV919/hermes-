package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Profile 是一套命名的 DMXAPI 配置（持久化到 ~/.DMXAPI/hermes/<sanitized>.json）。
type Profile struct {
	Name          string `json:"name"`
	BaseURL       string `json:"base_url"`
	APIKey        string `json:"api_key"`
	Model         string `json:"model"`
	ContextLength int    `json:"context_length,omitempty"` // 0 = 未设置 / 交 Hermes 自动探测
	SavedAt       string `json:"saved_at,omitempty"`
	AppVersion    string `json:"app_version,omitempty"`
}

// storeDir 返回配置库目录 ~/.DMXAPI/hermes，并确保其存在（限制权限）。
func storeDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".DMXAPI", "hermes")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// backupDir 返回备份目录 ~/.DMXAPI/hermes/backups（限制权限）。
func backupDir() (string, error) {
	base, err := storeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "backups")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// sanitizeName 把配置名转为安全的文件名：过滤路径穿越/控制字符/非法字符/Windows 保留名，截断 80 字符。
func sanitizeName(name string) string {
	name = strings.TrimSpace(name)
	var b strings.Builder
	for _, r := range name {
		if r < 0x20 || strings.ContainsRune(`/\:*?"<>|`, r) {
			b.WriteRune('_')
			continue
		}
		b.WriteRune(r)
	}
	s := strings.ReplaceAll(b.String(), "..", "_")
	s = strings.Trim(s, " .")
	if s == "" {
		s = "config"
	}
	upper := strings.ToUpper(s)
	for _, rsv := range []string{
		"CON", "PRN", "AUX", "NUL",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9",
	} {
		if upper == rsv {
			s = "_" + s
			break
		}
	}
	if rs := []rune(s); len(rs) > 80 {
		s = string(rs[:80])
	}
	return s
}

// profilePath 返回某配置名对应的 JSON 文件路径。
func profilePath(name string) (string, error) {
	dir, err := storeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, sanitizeName(name)+".json"), nil
}

// saveProfile 把配置写入 JSON（限制权限 0600）。
func saveProfile(p Profile) error {
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("配置名不能为空")
	}
	p.SavedAt = time.Now().Format(time.RFC3339)
	p.AppVersion = appVersion
	path, err := profilePath(p.Name)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// loadProfile 按配置名读取一套配置。
func loadProfile(name string) (Profile, error) {
	var p Profile
	path, err := profilePath(name)
	if err != nil {
		return p, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return p, fmt.Errorf("配置文件损坏 %s: %v", filepath.Base(path), err)
	}
	return p, nil
}

// listProfiles 列出全部已保存配置，按名称升序。
func listProfiles() ([]Profile, error) {
	dir, err := storeDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var profiles []Profile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var p Profile
		if json.Unmarshal(data, &p) != nil || strings.TrimSpace(p.Name) == "" {
			continue // 跳过损坏/非本工具的 json
		}
		profiles = append(profiles, p)
	}
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].Name < profiles[j].Name })
	return profiles, nil
}

// deleteProfile 删除一套配置。
func deleteProfile(name string) error {
	path, err := profilePath(name)
	if err != nil {
		return err
	}
	return os.Remove(path)
}

// deleteAllProfiles 删除配置库中全部命名配置（*.json），返回删除数量。不动 backups 子目录。
func deleteAllProfiles() (int, error) {
	dir, err := storeDir()
	if err != nil {
		return 0, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if os.Remove(filepath.Join(dir, e.Name())) == nil {
			n++
		}
	}
	return n, nil
}
