// Command swim-agent is the SWIM client agent.
//
//	swim-agent enroll --server URL --token TOKEN --pin SHA256   register this machine (or --ticket for a reimage)
//	swim-agent install                                          install + start the Windows service
//	swim-agent uninstall                                        stop + remove the service
//	swim-agent run                                              heartbeat loop (what the service runs)
//	swim-agent status                                           show enrollment + last heartbeat
//	swim-agent version
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"swim/agent/internal/agent"
)

func usage() {
	fmt.Fprintln(os.Stderr, `usage: swim-agent <enroll|install|uninstall|run|status|version>
  enroll   --server https://swim.example --token swim_et_... | --ticket swim_rt_... --pin <sha256 of server cert>`)
	os.Exit(2)
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "enroll":
		fs := flag.NewFlagSet("enroll", flag.ExitOnError)
		server := fs.String("server", "", "SWIM server URL")
		token := fs.String("token", "", "enrollment token")
		ticket := fs.String("ticket", "", "reimage ticket (same host after a SWIM reimage)")
		pin := fs.String("pin", "", "SHA-256 of the server TLS certificate (hex)")
		_ = fs.Parse(os.Args[2:])
		var st *agent.State
		if st, err = agent.Enroll(*server, *pin, *token, *ticket); err == nil {
			how := "new machine"
			if st.Reenrolled {
				how = "existing machine re-enrolled (history kept)"
			}
			fmt.Printf("enrolled: host_id=%s (%s)\n", st.HostID, how)
		}
	case "install":
		if err = agent.InstallService(); err == nil {
			fmt.Println("service SWIMAgent installed and running")
		}
	case "uninstall":
		if err = agent.UninstallService(); err == nil {
			fmt.Println("service SWIMAgent removed (enrollment kept in", agent.DataDir()+")")
		}
	case "run":
		lg, closer := agent.NewLogger()
		defer closer.Close()
		err = agent.RunService(func(ctx context.Context) error {
			e := agent.Run(ctx, lg)
			if errors.Is(e, agent.ErrRestart) {
				os.Exit(3) // abrupt exit = SCM recovery restarts the service (also in service mode)
			}
			if e != nil {
				lg.Printf("run: %v", e)
			}
			return e
		})
	case "status":
		var st *agent.State
		if st, err = agent.LoadState(); err == nil {
			st.SealedToken, st.SealedX25519 = "(sealed)", "(sealed)"
			b, _ := json.MarshalIndent(st, "", "  ")
			fmt.Println(string(b))
		}
	case "apply-update": // internal: run by the detached updater copy
		fs := flag.NewFlagSet("apply-update", flag.ExitOnError)
		staged := fs.String("staged", "", "verified new binary")
		exe := fs.String("exe", "", "installed agent path")
		_ = fs.Parse(os.Args[2:])
		err = agent.ApplyUpdate(*staged, *exe)
	case "version":
		fmt.Println("swim-agent", agent.Version)
	default:
		if run := agent.PluginSubcommand(os.Args[1]); run != nil { // a helper mode of a linked plugin
			os.Exit(run(os.Args[2:]))
		}
		usage()
	}
	if errors.Is(err, agent.ErrRestart) {
		// Exit without telling SCM we stopped: it counts as a failure and the
		// service's recovery action starts us again in ~10 s.
		os.Exit(3)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
