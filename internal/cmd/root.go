// Package cmd wires the reddit CLI commands (Cobra), mirroring twitter-cli's layout.
package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var jsonOut bool

// version is set at build time via -ldflags
// "-X github.com/yashiels/reddit-cli/internal/cmd.version=<tag>".
var version = "dev"

var rootCmd = &cobra.Command{
	Use:   "reddit",
	Short: "Reddit from the terminal, authenticating as the official Android app",
	Long: "reddit is a CLI for Reddit that authenticates using the official Android\n" +
		"app's OAuth client (extracted from the APK). It adds request jitter and\n" +
		"429 backoff to stay under rate limits. Personal use only — against ToS.",
	Version:       version,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	rootCmd.PersistentFlags().BoolVar(&jsonOut, "json", false, "emit raw JSON to stdout")
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// printJSON writes v as indented JSON to stdout (for --json).
func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
