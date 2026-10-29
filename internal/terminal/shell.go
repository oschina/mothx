package terminal

import "os"

// shellEnv returns the serve process environment unchanged. A terminal is a
// real user shell: filtering environment variables is both ineffective (the
// user can read the same credentials from files) and breaks proxies, SSH
// agents, cloud CLIs, and custom toolchains. We only fill in terminal defaults
// when the caller has not set them.
func shellEnv() []string {
	env := os.Environ()
	if os.Getenv("TERM") == "" {
		env = append(env, "TERM=xterm-256color")
	}
	if os.Getenv("COLORTERM") == "" {
		env = append(env, "COLORTERM=truecolor")
	}
	return env
}
