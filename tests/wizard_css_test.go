package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWizardContainerDoesNotOverflowLegacyHost(t *testing.T) {
	css, err := os.ReadFile(filepath.Join(repositoryRoot(t), "cmd", "lunapassport", "static", "wizard.css"))
	if err != nil {
		t.Fatalf("read wizard stylesheet: %v", err)
	}
	stylesheet := strings.ReplaceAll(string(css), "\r\n", "\n")
	if strings.Contains(stylesheet, ".wizard-container {\n  width: 100%;") {
		t.Fatal("wizard container must not use width: 100%: IE6 adds the border outside that width and shows a horizontal scrollbar")
	}
	if !strings.Contains(stylesheet, ".wizard-container {\n  width: auto;") {
		t.Fatal("wizard container must use width: auto so its border stays inside the legacy host viewport")
	}
}
