package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtractCommands(t *testing.T) {
	config = &Config{}
	commandRegex = nil
	commandRegexErr = nil

	answer := "Here are some commands:\n\n```bash\nls -la\necho \"hi\"\n```\n\nThen run:\n$ git status\n"

	commands := extractCommands(answer)
	require.Equal(t, []string{"ls -la", "echo \"hi\"", "git status"}, commands)
}
