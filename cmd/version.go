package cmd

import (
	"log"

	"github.com/spf13/cobra"
)

//*****************************************************************************
//
// This file is autogenerated by "go generate .". Do not modify.
//
//*****************************************************************************

var VersionString = "unset"
var StarterImageName = "ekaraplatform/installer:latest"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Returns the version details of the CLI.",
	Run: func(cmd *cobra.Command, args []string) {
		log.Println("CLI version details :")
		log.Println("")
		log.Printf("CLI version: %s\n", VersionString)
		log.Println("")
		log.Printf("Ekara installation based on the Docker image: %s\n", StarterImageName)
		log.Println("")
	},
}