package core

import (
	"os/exec"
	"strings"
	"testing"
)

// The core stays pure: no Bubble Tea, no ACP SDK, no processes.
func TestCoreImportsNothingImpure(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	for _, dep := range strings.Fields(string(out)) {
		if dep == "os/exec" || strings.HasPrefix(dep, "charm.land/") ||
			strings.HasPrefix(dep, "github.com/charmbracelet/") ||
			strings.HasPrefix(dep, "github.com/caelis-labs/") {
			t.Errorf("internal/core depends on %s", dep)
		}
	}
}
