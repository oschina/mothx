package platform

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestOS(t *testing.T) {
	os := OS()
	if os == "" {
		t.Error("expected non-empty OS")
	}

	if os != runtime.GOOS {
		t.Errorf("expected %s, got %s", runtime.GOOS, os)
	}
}

func TestIsWindows(t *testing.T) {
	expected := runtime.GOOS == "windows"
	if IsWindows() != expected {
		t.Errorf("expected %v, got %v", expected, IsWindows())
	}
}

func TestIsMacOS(t *testing.T) {
	expected := runtime.GOOS == "darwin"
	if IsMacOS() != expected {
		t.Errorf("expected %v, got %v", expected, IsMacOS())
	}
}

func TestIsLinux(t *testing.T) {
	expected := runtime.GOOS == "linux"
	if IsLinux() != expected {
		t.Errorf("expected %v, got %v", expected, IsLinux())
	}
}

func TestIsFreeBSD(t *testing.T) {
	expected := runtime.GOOS == "freebsd"
	if IsFreeBSD() != expected {
		t.Errorf("expected %v, got %v", expected, IsFreeBSD())
	}
}

func TestIsOpenBSD(t *testing.T) {
	expected := runtime.GOOS == "openbsd"
	if IsOpenBSD() != expected {
		t.Errorf("expected %v, got %v", expected, IsOpenBSD())
	}
}

func TestIsNetBSD(t *testing.T) {
	expected := runtime.GOOS == "netbsd"
	if IsNetBSD() != expected {
		t.Errorf("expected %v, got %v", expected, IsNetBSD())
	}
}

func TestIsDragonflyBSD(t *testing.T) {
	expected := runtime.GOOS == "dragonfly"
	if IsDragonflyBSD() != expected {
		t.Errorf("expected %v, got %v", expected, IsDragonflyBSD())
	}
}

func TestIsBSD(t *testing.T) {
	expected := false
	switch runtime.GOOS {
	case "freebsd", "openbsd", "netbsd", "dragonfly":
		expected = true
	}
	if IsBSD() != expected {
		t.Errorf("expected %v, got %v", expected, IsBSD())
	}
}

func TestIsSolaris(t *testing.T) {
	expected := false
	switch runtime.GOOS {
	case "solaris", "illumos":
		expected = true
	}
	if IsSolaris() != expected {
		t.Errorf("expected %v, got %v", expected, IsSolaris())
	}
}

func TestIsAIX(t *testing.T) {
	expected := runtime.GOOS == "aix"
	if IsAIX() != expected {
		t.Errorf("expected %v, got %v", expected, IsAIX())
	}
}

func TestIsPlan9(t *testing.T) {
	expected := runtime.GOOS == "plan9"
	if IsPlan9() != expected {
		t.Errorf("expected %v, got %v", expected, IsPlan9())
	}
}

func TestIsUnix(t *testing.T) {
	expected := runtime.GOOS != "windows" && runtime.GOOS != "plan9"
	if IsUnix() != expected {
		t.Errorf("expected %v, got %v", expected, IsUnix())
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Architecture tests
// ──────────────────────────────────────────────────────────────────────────────

func TestArch(t *testing.T) {
	if got := Arch(); got != runtime.GOARCH {
		t.Errorf("expected %s, got %s", runtime.GOARCH, got)
	}
}

func TestIsAMD64(t *testing.T) {
	expected := runtime.GOARCH == "amd64"
	if IsAMD64() != expected {
		t.Errorf("expected %v, got %v", expected, IsAMD64())
	}
}

func TestIsARM64(t *testing.T) {
	expected := runtime.GOARCH == "arm64"
	if IsARM64() != expected {
		t.Errorf("expected %v, got %v", expected, IsARM64())
	}
}

func TestIsARM(t *testing.T) {
	expected := runtime.GOARCH == "arm"
	if IsARM() != expected {
		t.Errorf("expected %v, got %v", expected, IsARM())
	}
}

func TestIs386(t *testing.T) {
	expected := runtime.GOARCH == "386"
	if Is386() != expected {
		t.Errorf("expected %v, got %v", expected, Is386())
	}
}

func TestIs64Bit(t *testing.T) {
	expected := false
	switch runtime.GOARCH {
	case "amd64", "arm64", "ppc64", "ppc64le", "mips64", "mips64le",
		"s390x", "riscv64", "loong64":
		expected = true
	}
	if Is64Bit() != expected {
		t.Errorf("expected %v, got %v", expected, Is64Bit())
	}
}

func TestIsLittleEndian(t *testing.T) {
	expected := false
	switch runtime.GOARCH {
	case "amd64", "arm64", "arm", "386", "riscv64", "loong64",
		"mips64le", "mipsle", "ppc64le":
		expected = true
	}
	if IsLittleEndian() != expected {
		t.Errorf("expected %v, got %v", expected, IsLittleEndian())
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Directory tests
// ──────────────────────────────────────────────────────────────────────────────

func TestHomeDir(t *testing.T) {
	home := HomeDir()
	if home == "" {
		t.Error("expected non-empty home directory")
	}

	// Should be an absolute path
	if !filepath.IsAbs(home) {
		t.Errorf("expected absolute path, got %s", home)
	}
}

func TestConfigDir(t *testing.T) {
	t.Setenv("MOTHX_DIR", "/tmp/test-mothx")
	dir := ConfigDir()
	if dir != "/tmp/test-mothx" {
		t.Errorf("expected '/tmp/test-mothx', got '%s'", dir)
	}

	t.Setenv("MOTHX_DIR", "")
	dir = ConfigDir()
	if dir == "" {
		t.Error("expected non-empty config dir")
	}

	// Should be an absolute path
	if !filepath.IsAbs(dir) {
		t.Errorf("expected absolute path, got %s", dir)
	}
}

func TestConfigDirIgnoresVibeCodingDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MOTHX_DIR", "")
	t.Setenv("VIBECODING_DIR", filepath.Join(home, ".vibecoding"))

	want := filepath.Join(home, ".mothx")
	if got := ConfigDir(); got != want {
		t.Fatalf("ConfigDir() = %q, want %q", got, want)
	}
	if ConfigDirOverridden() {
		t.Fatal("VIBECODING_DIR must not override the MothX configuration directory")
	}
}

func TestConfigDirForOS(t *testing.T) {
	home := filepath.Join(string(os.PathSeparator), "home", "tester")
	appData := filepath.Join(string(os.PathSeparator), "Users", "tester", "AppData", "Roaming")

	tests := []struct {
		name    string
		goos    string
		appData string
		want    string
	}{
		{
			name: "darwin defaults to home dot directory",
			goos: "darwin",
			want: filepath.Join(home, ".mothx"),
		},
		{
			name: "linux defaults to home dot directory",
			goos: "linux",
			want: filepath.Join(home, ".mothx"),
		},
		{
			name: "freebsd defaults to home dot directory",
			goos: "freebsd",
			want: filepath.Join(home, ".mothx"),
		},
		{
			name: "openbsd defaults to home dot directory",
			goos: "openbsd",
			want: filepath.Join(home, ".mothx"),
		},
		{
			name: "solaris defaults to home dot directory",
			goos: "solaris",
			want: filepath.Join(home, ".mothx"),
		},
		{
			name: "aix defaults to home dot directory",
			goos: "aix",
			want: filepath.Join(home, ".mothx"),
		},
		{
			name: "plan9 defaults to home dot directory",
			goos: "plan9",
			want: filepath.Join(home, ".mothx"),
		},
		{
			name:    "windows uses appdata when available",
			goos:    "windows",
			appData: appData,
			want:    filepath.Join(appData, "mothx"),
		},
		{
			name: "windows falls back to roaming appdata",
			goos: "windows",
			want: filepath.Join(home, "AppData", "Roaming", "mothx"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := configDirForOS(tt.goos, home, tt.appData); got != tt.want {
				t.Fatalf("configDirForOS() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDataDir(t *testing.T) {
	dir := DataDir()
	if dir == "" {
		t.Error("expected non-empty data dir")
	}
}

func TestCacheDir(t *testing.T) {
	dir := CacheDir()
	if dir == "" {
		t.Error("expected non-empty cache dir")
	}
}

func TestSessionDir(t *testing.T) {
	dir := SessionDir()
	if dir == "" {
		t.Error("expected non-empty session dir")
	}

	// Should contain "sessions"
	if !contains(dir, "sessions") {
		t.Errorf("expected dir to contain 'sessions', got %s", dir)
	}
}

func TestSkillsDir(t *testing.T) {
	dir := SkillsDir()
	if dir == "" {
		t.Error("expected non-empty skills dir")
	}

	// Should contain "skills"
	if !contains(dir, "skills") {
		t.Errorf("expected dir to contain 'skills', got %s", dir)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Shell tests
// ──────────────────────────────────────────────────────────────────────────────

func TestDefaultShell(t *testing.T) {
	shell := DefaultShell()
	if shell == "" {
		t.Error("expected non-empty shell")
	}

	if runtime.GOOS == "linux" && !isExecutableAbsolutePath(shell) {
		t.Errorf("expected an executable absolute Linux shell, got %s", shell)
	}
}

func TestDefaultShellForOS(t *testing.T) {
	tests := []struct {
		name      string
		goos      string
		execPaths map[string]bool
		lookPaths map[string]bool
		want      string
	}{
		{
			name:      "linux prefers bash when available",
			goos:      "linux",
			execPaths: map[string]bool{"/bin/bash": true},
			want:      "/bin/bash",
		},
		{
			name: "linux falls back to sh when bash is unavailable",
			goos: "linux",
			want: "/bin/sh",
		},
		{
			name:      "darwin falls back to sh when zsh is unavailable",
			goos:      "darwin",
			execPaths: map[string]bool{},
			want:      "/bin/sh",
		},
		{
			name:      "windows prefers powershell when available",
			goos:      "windows",
			lookPaths: map[string]bool{"powershell.exe": true},
			want:      "powershell.exe",
		},
		{
			name: "windows falls back to cmd",
			goos: "windows",
			want: "cmd.exe",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isExecutable := func(path string) bool { return tt.execPaths[path] }
			lookPath := func(name string) bool { return tt.lookPaths[name] }
			if got := defaultShellForOS(tt.goos, isExecutable, lookPath); got != tt.want {
				t.Fatalf("defaultShellForOS() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDefaultShellIgnoresRelativeShellEnv(t *testing.T) {
	t.Setenv("SHELL", "sh -c bad")

	if got := DefaultShell(); got == "sh -c bad" {
		t.Fatal("DefaultShell trusted relative SHELL env")
	}
}

func TestShellArgs(t *testing.T) {
	tests := []struct {
		shell    string
		command  string
		expected []string
	}{
		{"/bin/bash", "echo hello", []string{"-c", "echo hello"}},
		{"/bin/zsh", "echo hello", []string{"-c", "echo hello"}},
		{"busybox-windows-x86_64.exe", "echo hello", []string{"sh", "-c", "echo hello"}},
		{"powershell.exe", "echo hello", []string{"-NoProfile", "-NonInteractive", "-Command", "echo hello"}},
		{"pwsh.exe", "echo hello", []string{"-NoProfile", "-NonInteractive", "-Command", "echo hello"}},
		{"cmd.exe", "echo hello", []string{"/c", "echo hello"}},
		{"/bin/rc", "echo hello", []string{"-c", "echo hello"}},
		{"/usr/local/bin/command-runner", "echo hello", []string{"-c", "echo hello"}},
		{"/usr/local/bin/arch", "echo hello", []string{"-c", "echo hello"}},
	}

	for _, tt := range tests {
		args := ShellArgs(tt.shell, tt.command)
		if len(args) != len(tt.expected) {
			t.Errorf("expected %d args, got %d", len(tt.expected), len(args))
			continue
		}

		for i, arg := range args {
			if arg != tt.expected[i] {
				t.Errorf("expected arg[%d] = '%s', got '%s'", i, tt.expected[i], arg)
			}
		}
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Path tests
// ──────────────────────────────────────────────────────────────────────────────

func TestPathSeparator(t *testing.T) {
	sep := PathSeparator()
	if sep == "" {
		t.Error("expected non-empty path separator")
	}

	if runtime.GOOS == "windows" {
		if sep != "\\" {
			t.Errorf("expected '\\', got '%s'", sep)
		}
	} else {
		if sep != "/" {
			t.Errorf("expected '/', got '%s'", sep)
		}
	}
}

func TestJoinPath(t *testing.T) {
	path := JoinPath("home", "user", "test")
	if path == "" {
		t.Error("expected non-empty path")
	}

	expected := filepath.Join("home", "user", "test")
	if path != expected {
		t.Errorf("expected '%s', got '%s'", expected, path)
	}
}

func TestNormalizePath(t *testing.T) {
	if runtime.GOOS == "windows" {
		path := NormalizePath("home/user/test")
		if path != "home\\user\\test" {
			t.Errorf("expected 'home\\user\\test', got '%s'", path)
		}
	} else {
		path := NormalizePath("home/user/test")
		if path != "home/user/test" {
			t.Errorf("expected 'home/user/test', got '%s'", path)
		}
	}
}

func TestExpandHome(t *testing.T) {
	home := HomeDir()

	tests := []struct {
		input    string
		expected string
	}{
		{"~/test", filepath.Join(home, "test")},
		{"~", home},
		{"/absolute/path", "/absolute/path"},
		{"relative/path", "relative/path"},
	}

	for _, tt := range tests {
		result := ExpandHome(tt.input)
		if result != tt.expected {
			t.Errorf("ExpandHome(%s) = %s, expected %s", tt.input, result, tt.expected)
		}
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Platform-specific paths and environment tests
// ──────────────────────────────────────────────────────────────────────────────

func TestCommonPaths(t *testing.T) {
	paths := CommonPaths()
	if len(paths) == 0 {
		t.Error("expected non-empty common paths")
	}

	// Should have home directory
	if _, ok := paths["home"]; !ok {
		t.Error("expected 'home' in common paths")
	}

	// Should have temp directory
	if _, ok := paths["temp"]; !ok {
		t.Error("expected 'temp' in common paths")
	}
}

func TestSandboxPaths(t *testing.T) {
	paths := SandboxPaths()
	// On unsupported platforms, empty is fine
	if runtime.GOOS == "linux" && len(paths) == 0 {
		t.Error("expected non-empty sandbox paths on linux")
	}
}

func TestDeniedPaths(t *testing.T) {
	paths := DeniedPaths()
	if runtime.GOOS != "windows" && runtime.GOOS != "plan9" && len(paths) == 0 {
		t.Error("expected non-empty denied paths")
	}
}

func TestDefaultEnvVars(t *testing.T) {
	vars := DefaultEnvVars()
	if len(vars) == 0 {
		t.Error("expected non-empty env vars")
	}

	// Should have PATH (or "path" on Plan 9)
	found := false
	for _, v := range vars {
		if v == "PATH" || v == "path" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'PATH' in env vars")
	}
}

func TestTempDir(t *testing.T) {
	dir := TempDir()
	if dir == "" {
		t.Error("expected non-empty temp dir")
	}
}

func TestExecutableExt(t *testing.T) {
	ext := ExecutableExt()
	if runtime.GOOS == "windows" {
		if ext != ".exe" {
			t.Errorf("expected '.exe', got '%s'", ext)
		}
	} else {
		if ext != "" {
			t.Errorf("expected empty string, got '%s'", ext)
		}
	}
}

func TestIsExecutable(t *testing.T) {
	// Test with executable file
	executable := os.FileMode(0755)
	if !IsExecutable(executable) {
		t.Error("expected executable file to be executable")
	}

	// Test with non-executable file
	nonExecutable := os.FileMode(0644)
	if runtime.GOOS != "windows" {
		if IsExecutable(nonExecutable) {
			t.Error("expected non-executable file to not be executable")
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
