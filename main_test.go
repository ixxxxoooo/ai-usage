package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ixxxxoooo/ai-usage/internal/gui"
)

// TestParseSelfTest 验证命令行自检参数解析。
func TestParseSelfTest(t *testing.T) {
	s := parseSelfTest([]string{"--selftest=position", "--selftest-out=/tmp/x.json", "--selftest-bounds=1,2,3,4", "--other"})
	if s.mode != "position" || s.out != "/tmp/x.json" || s.bounds != "1,2,3,4" {
		t.Errorf("%+v", s)
	}
	s = parseSelfTest([]string{"--selftest=panel-get"})
	if s.mode != "panel-get" || filepath.Base(s.out) != "aiusage-selftest.json" {
		t.Errorf("默认输出路径: %+v", s)
	}
	if s := parseSelfTest(nil); s.mode != "" || s.out != "" {
		t.Errorf("无参数应为空: %+v", s)
	}
}

// TestDataDirEnv 验证 AIUSAGE_DATA_DIR 覆盖数据目录并标记为测试目录。
func TestDataDirEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AIUSAGE_DATA_DIR", dir)
	got, isTest := dataDir()
	if got != dir || !isTest {
		t.Errorf("%q %v", got, isTest)
	}
	if _, err := os.Stat(got); err != nil {
		t.Error(err)
	}
}

// TestVersionMatchesMygoJSON 代码里的版本号必须与 mygo.json 一致（发布流程以 mygo.json 为准）。
func TestVersionMatchesMygoJSON(t *testing.T) {
	b, err := os.ReadFile("mygo.json")
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Version != gui.AppVersion {
		t.Errorf("mygo.json 版本 %q 与 gui.AppVersion %q 不一致", cfg.Version, gui.AppVersion)
	}
}
