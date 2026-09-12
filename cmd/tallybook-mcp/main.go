// Command tallybook-mcp serves tallybook's ledger over the Model Context
// Protocol on stdio, so an agent can ask about its own spend mid-session.
//
// Everything behind the protocol lives in internal/query: the store, the
// price table, the scan throttle and the eight queries themselves. This
// package is the MCP transport over that — tool names, schemas,
// annotations, the lock each call is made under, and the text content a
// model reads. tallybook --serve is the same application layer behind HTTP,
// so the two cannot answer the same question differently.
//
// It reads the same database the tallybook CLI writes and reuses the same
// pricing, parsing and findings packages, so the two report the same
// numbers. It is read-only apart from the database updates ingest makes,
// makes no network calls, and never returns prompt text or tool output.
//
// stdout carries JSON-RPC only; every log line goes to stderr.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Set by GoReleaser via -ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	log.SetFlags(0)
	log.SetOutput(os.Stderr)
	log.SetPrefix("tallybook-mcp: ")

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--version", "-v", "version":
			fmt.Fprintf(os.Stderr, "tallybook-mcp %s (%s, %s)\n", version, commit, date)
			return
		default:
			log.Printf("unknown argument %q; tallybook-mcp takes no arguments and speaks MCP on stdio", os.Args[1])
			os.Exit(2)
		}
	}

	a, err := newApp()
	if err != nil {
		log.Print(err)
		os.Exit(1)
	}
	// Scan in the background so initialize is answered at once; a tool call
	// that arrives first waits for, or performs, the same first scan. The
	// scan is waited for before Close so a client that disconnects at once
	// cannot leave the database closed under it.
	warmed := make(chan struct{})
	go func() {
		a.Warm()
		close(warmed)
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err = newServer(a, version).Run(ctx, &mcp.StdioTransport{})
	<-warmed
	a.Close()
	if err != nil && ctx.Err() == nil {
		log.Print(err)
		os.Exit(1)
	}
}
