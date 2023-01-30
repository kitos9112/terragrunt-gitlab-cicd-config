package main

import "github.com/kitos9112/terragrunt-gitlab-cicd-config/cmd"

// This variable is set at build time using -ldflags parameters.
// But we still set a default here for those using plain `go get` downloads
// For more info, see: http://stackoverflow.com/a/11355611/483528
var VERSION string = "2.0.0"

func main() {
	cmd.Execute(VERSION)
}
