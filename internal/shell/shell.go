// Package shell embeds the shell integration scripts printed by `recap shell-init`.
package shell

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed zsh.sh
var zsh string

//go:embed bash.sh
var bash string

// Script returns the integration script for a shell.
func Script(name string) (string, error) {
	switch name {
	case "zsh":
		return zsh, nil
	case "bash":
		return bash, nil
	case "fish":
		return "", fmt.Errorf("fish support is planned for the MVP (SPEC D-06)")
	}
	return "", fmt.Errorf("unknown shell %q (zsh, bash)", name)
}

// Line is what goes into the rc file.
func Line(name string) string { return `eval "$(recap shell-init ` + name + `)"` }

// Detect returns the user's login shell from $SHELL (zsh or bash), default zsh.
func Detect() string {
	if strings.HasSuffix(os.Getenv("SHELL"), "bash") {
		return "bash"
	}
	return "zsh"
}

// RCPath is the interactive rc file for a shell (ZDOTDIR is honored for zsh).
func RCPath(name string) string {
	home, _ := os.UserHomeDir()
	if name == "bash" {
		return filepath.Join(home, ".bashrc")
	}
	if z := os.Getenv("ZDOTDIR"); z != "" {
		return filepath.Join(z, ".zshrc")
	}
	return filepath.Join(home, ".zshrc")
}

// Installed reports whether the rc file already loads recap.
func Installed(name string) bool {
	b, err := os.ReadFile(RCPath(name))
	return err == nil && strings.Contains(string(b), "recap shell-init")
}

// Install appends the eval line to the rc file once. It never rewrites
// existing content.
func Install(name string) (path string, added bool, err error) {
	if _, err := Script(name); err != nil {
		return "", false, err
	}
	path = RCPath(name)
	if Installed(name) {
		return path, false, nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return path, false, err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "\n# recap: record commands in attached tabs\n%s\n", Line(name))
	return path, err == nil, err
}
