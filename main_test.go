package main

import (
	"os"
	"path/filepath"
	"testing"
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
