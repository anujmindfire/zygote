// Package main provides the CLI for Zygote.
package main

import (
	"fmt"
	"os"

	"github.com/mindfire/zygote/trace"
	"github.com/mindfire/zygote/vfs"
	"github.com/spf13/cobra"
)

var stepsCmd = &cobra.Command{
	Use:   "steps [run.zip]",
	Short: "List steps in a recording",
	Args:  cobra.ExactArgs(1),
	Run: func(_ *cobra.Command, args []string) {
		bundlePath := args[0]
		store := vfs.NewMemStore()

		bundle, err := trace.ImportFile(bundlePath, store)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(2) // bundle invalid
		}

		tree := trace.BuildStepTree(bundle.Recording.Steps)

		fmt.Printf("%-5s %-30s %-12s %-8s %s\n", "STEP", "NAME", "HASH", "EFFECTS", "ACTOR")
		printStepTree(tree, "")
	},
}

func printStepTree(steps []trace.Step, indent string) {
	for i, s := range steps {
		hashStr := s.Root
		if len(hashStr) > 8 {
			hashStr = hashStr[:8]
		}
		actorStr := s.Actor
		if actorStr != "" {
			actorStr = "[" + actorStr + "]"
		}

		var linePrefix string
		var nextIndent string
		if indent != "" {
			if i == len(steps)-1 {
				linePrefix = indent + "└── "
				nextIndent = indent + "    "
			} else {
				linePrefix = indent + "├── "
				nextIndent = indent + "│   "
			}
		} else {
			if len(s.Children) > 0 {
				nextIndent = "  "
			}
		}

		displayName := linePrefix + s.Name
		fmt.Printf("%-5d %-30s %-12s %-8d %s\n", s.N, displayName, hashStr, s.Effects, actorStr)
		if len(s.Children) > 0 {
			printStepTree(s.Children, nextIndent)
		}
	}
}

func init() {
	rootCmd.AddCommand(stepsCmd)
}
