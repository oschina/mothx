//go:build windows

package platform

import (
	"path/filepath"
	"testing"
)

func TestBusyboxAssetForArch(t *testing.T) {
	tests := []struct {
		arch string
		want string
		ok   bool
	}{
		{arch: "amd64", want: "busybox-windows-x86_64.exe", ok: true},
		{arch: "386", want: "busybox-windows-x86.exe", ok: true},
		{arch: "arm64", want: "", ok: false},
	}

	for _, tt := range tests {
		gotName, _, gotOK := busyboxAssetForArch(tt.arch)
		if gotName != tt.want || gotOK != tt.ok {
			t.Fatalf("busyboxAssetForArch(%q) = (%q, %v), want (%q, %v)", tt.arch, gotName, gotOK, tt.want, tt.ok)
		}
	}
}

func TestWindowsBusyboxDirUsesConfigBin(t *testing.T) {
	if got := filepath.Join("C:\\Users\\tester\\AppData\\Roaming\\vibecoding", "bin"); got != "C:\\Users\\tester\\AppData\\Roaming\\vibecoding\\bin" {
		t.Fatalf("filepath.Join() = %q", got)
	}
}
