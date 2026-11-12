package localcodex

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsDaemonCompatibilityAndResume(t *testing.T) {
	for _, supported := range []bool{true, false} {
		t.Run(map[bool]string{true: "new", false: "legacy"}[supported], func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "codex-test.ps1")
			help := "legacy"
			if supported {
				help = "--no-daemon"
			}
			script := "if ($args -contains '--help') { Write-Output '" + help + "'; exit 0 }; $args | ConvertTo-Json -Compress"
			if err := os.WriteFile(bin, []byte(script), 0600); err != nil {
				t.Fatal(err)
			}
			for _, resume := range []bool{false, true} {
				// Match Launch: retain the user's execution policy. This locally
				// generated fixture does not need an execution-policy override.
				out, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-EncodedCommand", encodedCommand(windowsTerminalScript(Options{Directory: dir, Resume: resume}, bin))).CombinedOutput()
				if err != nil {
					t.Fatalf("%v: %s", err, out)
				}
				if strings.Contains(string(out), "--no-daemon") != supported {
					t.Fatalf("wrong compatibility: %s", out)
				}
				if strings.Contains(string(out), "resume") != resume {
					t.Fatalf("resume lost: %s", out)
				}
			}
		})
	}
}
