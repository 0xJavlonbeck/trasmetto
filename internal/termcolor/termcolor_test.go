package termcolor

import (
	"os"
	"strings"
	"testing"
)

func TestStatusColors(t *testing.T) {
	cases := map[int]string{200: Green, 301: Cyan, 404: Yellow, 500: Red}
	for code, want := range cases {
		got := Status(code, true)
		if !strings.HasPrefix(got, want) || !strings.Contains(got, "\x1b[0m") {
			t.Errorf("Status(%d) = %q, want %q prefix + reset", code, got, want)
		}
	}
	if got := Status(200, false); got != "200" {
		t.Errorf("Status(200,false) = %q, want plain 200", got)
	}
	if got := Status(42, true); !strings.Contains(got, " 42") {
		t.Errorf("Status(42) = %q, want width-3 alignment", got)
	}
}

func TestWrapRespectsToggle(t *testing.T) {
	if got := Wrap(false, Red, "x"); got != "x" {
		t.Errorf("Wrap(off) = %q, want plain", got)
	}
	if got := Wrap(true, Red, "x"); got != Red+"x"+"\x1b[0m" {
		t.Errorf("Wrap(on) = %q", got)
	}
}

func TestEnabledRespectsNoColorEnv(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	if Enabled(false) {
		t.Error("Enabled should be false when NO_COLOR is set")
	}
	t.Setenv("NO_COLOR", "")
	if Enabled(true) {
		t.Error("Enabled should be false when the --no-color flag is set")
	}
	// A pipe (this test's stdout) is not a char device, so IsTerminal is false.
	if IsTerminal(os.Stdout) && Enabled(false) == false {
		t.Skip("running attached to a terminal")
	}
}
