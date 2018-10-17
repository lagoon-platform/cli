package main

import "log"

//*****************************************************************************
//
// This file is autogenerated by "go generate .". Do not modify.
//
//*****************************************************************************

var VersionString = "unset"

// runVersion returns the details of the CLI version
func runVersion() {
	log.Println("CLI version details :")
	log.Println("")
	log.Printf("CLI version: %s\n", VersionString)
	log.Println("")
	log.Printf("Ekara installation based on the Docker image: %s\n", starterImageName)
	log.Println("")
}
