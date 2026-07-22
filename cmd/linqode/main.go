// Command linqode is an SSH TUI for monitoring and operating remote servers.
// During the Go port (milestone G0) it only resolves the connection target;
// connecting and the TUI land with G1.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/gualask/linqode/internal/config"
	"github.com/gualask/linqode/internal/remote"
)

func main() {
	configPath := flag.String("config", "", "config file (default ~/.config/linqode/config.toml)")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: linqode [flags] [host]\n\n"+
			"host is a name from the config file or an inline [user@]host[:port];\n"+
			"it can be omitted with exactly one configured host.\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() > 1 {
		flag.Usage()
		os.Exit(2)
	}

	if err := run(*configPath, flag.Arg(0)); err != nil {
		fmt.Fprintf(os.Stderr, "linqode: %v\n", err)
		os.Exit(1)
	}
}

func run(configPath, hostArg string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	sel, err := cfg.Select(hostArg)
	if err != nil {
		return err
	}
	target, err := remote.Resolve(sel.Spec)
	if err != nil {
		return err
	}
	fmt.Printf("resolved %s@%s:%d (known_hosts name %q, %d identity file(s))\n",
		target.User, target.Host, target.Port, target.DisplayHost, len(target.IdentityFiles))
	fmt.Println("connecting is not implemented yet — it lands with milestone G1")
	return nil
}
