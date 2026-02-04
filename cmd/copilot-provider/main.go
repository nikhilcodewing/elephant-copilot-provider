package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const version = "0.1.0"

const configTemplate = `# ~/.config/elephant/copilot.toml
enabled = true
cli_mode = "both" # auto|copilot|gh|both
default_model = "claude-sonnet-4.5"
models = ["claude-sonnet-4.5", "gpt-5.2-codex", "gpt-4.1"]
copilot_args = ["--no-color", "--silent"]
gh_copilot_args = ["--no-color", "--silent"]
clipboard_cmd = "wl-copy"
terminal_prefill_cmd = "bash -lc 'read -e -i %CMD% -p \">>> \" cmd; exec $SHELL'"
`

func main() {
	rootCmd := &cobra.Command{
		Use:   "copilot-provider",
		Short: "Helper utilities for the Elephant Copilot provider",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			viper.SetEnvPrefix("ELEPHANT_COPILOT")
			viper.AutomaticEnv()
		},
	}

	rootCmd.AddCommand(&cobra.Command{
		Use:   "print-config",
		Short: "Print a sample copilot.toml",
		Run: func(cmd *cobra.Command, args []string) {
			header := viper.GetString("CONFIG_HEADER")
			if header != "" {
				fmt.Println(header)
			}
			fmt.Print(configTemplate)
		},
	})

	rootCmd.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println(version)
		},
	})

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
