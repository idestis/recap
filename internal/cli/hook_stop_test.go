package cli

import "testing"

// Invariant 12: the Stop hook never fails, whatever it is given.
func TestClaudeStopHookIsSilentOnErrors(t *testing.T) {
	t.Setenv("RECAP_HOME", t.TempDir())
	for _, sess := range []string{"", "no-such-session"} {
		t.Setenv("RECAP_SESSION", sess)
		c := hookCmd()
		c.SetArgs([]string{"claude-stop"})
		c.SilenceUsage, c.SilenceErrors = true, true
		if err := c.Execute(); err != nil {
			t.Fatalf("session %q: %v", sess, err)
		}
	}
}
