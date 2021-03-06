package cmd

import (
	"fmt"
	"github.com/ekara-platform/cli/common"
	"github.com/ekara-platform/engine/action"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"io/ioutil"
	"os"
)

func init() {
	// This is a descriptor-based command
	applyDescriptorFlags(dumpCmd)

	rootCmd.AddCommand(dumpCmd)
}

var dumpCmd = &cobra.Command{
	Use:   "dump <repository-url>",
	Short: "Dump an existing environment descriptor.",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		color.New(color.FgHiWhite).Println(common.LOG_DUMPING_ENV)
		dir, err := ioutil.TempDir(os.TempDir(), "ekara_dump")
		if err != nil {
			common.CliFeedbackNotifier.Error("Unable to create temporary directory: %s", err.Error())
			os.Exit(1)
		}
		defer os.RemoveAll(dir)

		e := initLocalEngine(dir, args[0])
		res, err := e.Execute(action.DumpActionID)
		if err != nil {
			common.CliFeedbackNotifier.Error("Unable to run dump action: %s", err.Error())
			os.Exit(1)
		}

		envYaml, err := res.(action.DumpResult).AsYaml()
		if err != nil {
			common.CliFeedbackNotifier.Error("Unable to format YAML dump: %s", err.Error())
			os.Exit(1)
		}
		fmt.Println("---")
		fmt.Println(string(envYaml))
		os.Exit(0)
	},
}
