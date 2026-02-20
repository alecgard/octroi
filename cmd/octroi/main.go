package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var cfgFile string

var rootCmd = &cobra.Command{
	Use:   "octroi",
	Short: "Octroi — Agent Tool Gateway",
	Long:  "Octroi is a gateway that sits between AI agents and the tools/APIs they consume, providing discovery, authenticated proxying, rate limiting, budget enforcement, and usage metering.",
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: configs/octroi.yaml)")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
