package common

import (
	"log/slog"
	"os/exec"
	"strings"
)

func ReplaceResultOrStdinCmd(replace, result string) *exec.Cmd {
	if !strings.Contains(replace, "%VALUE%") {
		cmd := exec.Command("sh", "-c", replace)

		cmd.Stdin = strings.NewReader(result)
		return cmd
	}

	return exec.Command("sh", "-c", strings.ReplaceAll(replace, "%VALUE%", result))
}

func ClipboardText() string {
	cmd := exec.Command("wl-paste", "-t", "text", "-n")

	out, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "Nothing is copied") {
			return ""
		}

		slog.Error("replaceresultorstdin", "get clipboard", err)

		return ""
	}

	return strings.TrimSpace(string(out))
}
