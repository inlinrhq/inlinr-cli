// Command inlinr is the Inlinr tracking daemon: it accepts heartbeats from
// editor plugins, buffers them in an on-disk SQLite queue, and uploads batches
// to the Inlinr ingest endpoint.
//
// Subcommands:
//
//	inlinr activate            run the Device flow and write a token to config
//	inlinr heartbeat [flags]   enqueue a heartbeat (optionally flush) from a plugin
//	inlinr sync-commits [flags] send local git line counts (opt-in, never automatic)
//	inlinr sync-deps [flags]   send npm manifests of a project followed on Inlinr
//	inlinr mcp                 serve the Model Context Protocol to an AI assistant
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
	case "sync-commits":
		os.Exit(runSyncCommits(os.Args[2:]))
	case "sync-deps":
		// Spawned in the background by heartbeat: a failure is a log line,
		// never an exit code someone has to look at.
		if err := runSyncDeps(os.Args[2:]); err != nil {
			fmt.Fprintf(logSink, "inlinr: %v\n", err)
		}
	case "sync-ai":
		// Retired: the Claude Code plugin parses its own transcripts now and
		// never calls this binary. The command stays as a no-op because early
		// plugin releases ran it from a Stop hook, and a hook that exits
		// non-zero interrupts the user's turn. It must keep exiting 0.
		fmt.Fprintln(os.Stderr, "inlinr: sync-ai is retired; update the Inlinr plugin for Claude Code, which tracks AI sessions itself")
	case "mcp":
		if err := runMCP(os.Args[2:]); err != nil {
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
  inlinr sync-commits [flags]     Send line counts from local git history.
  inlinr sync-deps [flags]        Send npm manifests, if the project's dependencies are followed.
                                  Counts only — no messages, no file paths.
                                  Run it yourself; nothing calls it for you.
  inlinr mcp                      Serve Inlinr's tools to an AI assistant over stdio (MCP).
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
