package commands

import (
	"testing"

	cliout "github.com/nicolasacchi/clicore/output"
)

func TestDefaultRowLimit(t *testing.T) {
	t.Run("human default is 1000", func(t *testing.T) {
		t.Setenv("CLAUDECODE", "")
		if got := defaultRowLimit(); got != 1000 {
			t.Errorf("human default = %d, want 1000", got)
		}
	})

	t.Run("agent default is AgentRowCap", func(t *testing.T) {
		t.Setenv("CLAUDECODE", "1")
		if got := defaultRowLimit(); got != cliout.AgentRowCap {
			t.Errorf("agent default = %d, want %d", got, cliout.AgentRowCap)
		}
	})
}
