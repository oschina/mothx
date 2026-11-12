package main

import (
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestParseLaunchBinaryArg(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
		ok   bool
	}{
		{"equals", []string{"--mothx-bin=/opt/mothx"}, "/opt/mothx", true},
		{"separate", []string{"--other", "--mothx-bin", "/opt/mothx"}, "/opt/mothx", true},
		{"missing value", []string{"--mothx-bin"}, "", false},
		{"next flag", []string{"--mothx-bin", "--verbose"}, "", false},
		{"absent", []string{"--verbose"}, "", false},
		{"trimmed", []string{"--mothx-bin=  /opt/mothx  "}, "/opt/mothx", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseLaunchBinaryArg(tc.args)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("parseLaunchBinaryArg(%v) = %q,%v want %q,%v", tc.args, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestLaunchBinaryOverrideWinsAndStaysLocal(t *testing.T) {
	previous := launchBinaryOverride
	t.Cleanup(func() { launchBinaryOverride = previous })

	t.Setenv("MOTHX_BIN", "/env/mothx")
	setLaunchBinaryOverride("/launch/mothx")
	bin, source := resolveMothxBinarySource()
	abs, _ := filepath.Abs("/launch/mothx")
	if bin != abs || source != "启动参数 --mothx-bin" {
		t.Fatalf("launch override resolution = %q, %q", bin, source)
	}

	setLaunchBinaryOverride("")
	bin, source = resolveMothxBinarySource()
	if bin != "/env/mothx" || source != "MOTHX_BIN" {
		t.Fatalf("env fallback resolution = %q, %q", bin, source)
	}
}

func TestRuntimePaneProjectsConnectionDoctorAndLogs(t *testing.T) {
	a := testApp()
	a.dark = true
	a.setFeatures(map[string]bool{"doctor": true})
	a.settingsOpen = true
	a.settingsCategory = "system"
	a.settingsTab = "runtime"
	a.conn = connReady
	a.stderr = []string{"warning: test stderr"}
	a.runtime.Doctor = &doctorResultView{
		Version: "1.2.3",
		Checks: []doctorCheckView{
			{ID: "provider", Title: "Provider", Status: "ok", Detail: "configured"},
			{ID: "sandbox", Title: "Sandbox", Status: "warning", Detail: "not mounted", Fix: "install bwrap"},
		},
	}
	a.runtime.DoctorStatus = "诊断完成"
	tt := ui.NewTester(a.view, 1180, 1000)
	for _, want := range []string{"连接", "重启连接", "诊断", "运行诊断", "Provider · 正常", "configured", "Sandbox · 警告", "修复：install bwrap", "ACP 日志", "warning: test stderr"} {
		if !textPresent(tt, want) {
			t.Fatalf("runtime pane missing %q: %q", want, tt.Texts())
		}
	}
	if !a.doctorCapable() {
		t.Fatal("doctor feature must be exposed when advertised")
	}
	if got := doctorStatusLabel("failed"); got != "失败" {
		t.Fatalf("failed label = %q", got)
	}
}
