package main

import (
	"fmt"
	"github.com/lambdawalker/go.apexfission.aws.email-verification/config"
	"os"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: verify-config CONFIG.json")
		os.Exit(2)
	}
	f, e := os.Open(os.Args[1])
	if e == nil {
		defer f.Close()
		_, e = config.Load(f)
	}
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	fmt.Println("configuration valid")
}
