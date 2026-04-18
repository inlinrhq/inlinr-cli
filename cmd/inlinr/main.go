// Command inlinr is the Inlinr tracking daemon: it accepts heartbeats from
// editor plugins, buffers them in an on-disk SQLite queue, and uploads batches
// to the Inlinr ingest endpoint.
//
// Subcommands:
//
//	inlinr activate            run the Device flow and write a token to config
//	inlinr heartbeat [flags]   enqueue a heartbeat (optionally flush) from a plugin
//	inlinr doctor              print config + ping server
//	inlinr --version
package main

import (
	"fmt"
	"os"
)

// Version is set at build time via -ldflags "-X main.Version=...".
var Version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "activate":
		if err := runActivate(os.Args[2:]); err != nil {
			fail(err)
		}
	case "heartbeat":
		if err := runHeartbeat(os.Args[2:]); err != nil {
			fail(err)
		}
	case "doctor":
		if err := runDoctor(os.Args[2:]); err != nil {
			fail(err)
		}
	case "signout":
		if err := runSignout(os.Args[2:]); err != nil {
			fail(err)
		}
	case "upgrade":
		if err := runUpgrade(os.Args[2:]); err != nil {
			fail(err)
		}
	case "--version", "-v", "version":
		fmt.Println(Version)
	case "--help", "-h", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `inlinr — time tracking daemon

Usage:
  inlinr activate                 Run the Device flow to authorize this machine.
  inlinr heartbeat [flags]        Enqueue a heartbeat and flush the queue.
  inlinr signout                  Revoke this device token (server + local).
  inlinr upgrade                  Download and install the latest release.
  inlinr doctor                   Print config and check server connectivity.
  inlinr --version                Print version.

Run 'inlinr heartbeat --help' for the full heartbeat flag list.
`)
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "inlinr: %v\n", err)
	os.Exit(1)
}
