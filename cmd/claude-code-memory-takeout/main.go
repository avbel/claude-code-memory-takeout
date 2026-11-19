package main

import (
	"os"
	"runtime/debug"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = ""

func main() {
	a := &app{
		stdout:  os.Stdout,
		stderr:  os.Stderr,
		getenv:  os.Getenv,
		version: versionString(),
	}
	os.Exit(a.Run(os.Args[1:]))
}

func versionString() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}
