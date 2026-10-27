package cli

import (
	"errors"
	"os"
	"strings"

	"github.com/charmbracelet/huh"
)

// interactive is the single gate for prompts: a TTY on both ends, and no
// --yes, --json, --dry-run or CI. Every prompt has a flag path (invariant 10).
// Help and plain output never use Charm; huh appears only for real choices.
func interactive() bool {
	if flagYes || flagJSON || os.Getenv("CI") != "" {
		return false
	}
	return isTTY(os.Stdin) && isTTY(os.Stdout)
}

type option struct{ value, label string }

// choose asks for one option, or returns def when not interactive.
func choose(title string, opts []option, def string) string {
	if !interactive() {
		return def
	}
	v := def
	var ho []huh.Option[string]
	for _, o := range opts {
		ho = append(ho, huh.NewOption(o.label, o.value))
	}
	err := huh.NewSelect[string]().Title(title).Options(ho...).Value(&v).Run()
	if errors.Is(err, huh.ErrUserAborted) {
		return def
	}
	return v
}

// pick is a multi-select with preselected values. ok is false when the user aborts.
func pick(title, desc string, opts []option, selected []string) (out []string, ok bool) {
	out = selected
	var ho []huh.Option[string]
	for _, o := range opts {
		ho = append(ho, huh.NewOption(o.label, o.value))
	}
	err := huh.NewMultiSelect[string]().Title(title).Description(desc).Options(ho...).Value(&out).Height(min(len(opts)+2, 16)).Run()
	if err != nil {
		return selected, false
	}
	return out, true
}

// confirm asks yes/no with huh; non-interactive callers use flags instead.
func confirm(title string, def bool) (bool, error) {
	v := def
	err := huh.NewConfirm().Title(title).Affirmative("Yes").Negative("No").Value(&v).Run()
	if errors.Is(err, huh.ErrUserAborted) {
		return false, errors.New("cancelled")
	}
	return v, err
}

// input asks for one required value.
func input(title, desc string) (string, error) {
	var v string
	err := huh.NewInput().Title(title).Description(desc).Value(&v).
		Validate(func(s string) error {
			if strings.TrimSpace(s) == "" {
				return errors.New("required")
			}
			return nil
		}).Run()
	if errors.Is(err, huh.ErrUserAborted) {
		return "", errors.New("cancelled")
	}
	return strings.TrimSpace(v), err
}
