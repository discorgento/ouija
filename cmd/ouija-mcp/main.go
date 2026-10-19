// Command ouija-mcp runs ouija's Model Context Protocol server over stdio. It is
// a thin wrapper around internal/mcp, kept so harness configs that launch
// `ouija-mcp` keep working — `ouija mcp` runs the same server.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/discorgento/ouija/internal/mcp"
	"github.com/discorgento/ouija/internal/version"

	_ "github.com/discorgento/ouija/totems" // auto-registers all totems via go:generate
)

func main() {
	// Answer and exit before touching stdio: harnesses and packagers probe the
	// binary this way, and starting the JSON-RPC server there would hang them.
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Printf("ouija-mcp %s\n", version.Version)
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv, err := mcp.New(func(f string, a ...any) {
		fmt.Fprintf(os.Stderr, "[ouija-mcp] "+f+"\n", a...)
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ouija-mcp] %v\n", err)
		os.Exit(1)
	}
	srv.Logf("%s started (%d modules)", mcp.ServerName(), len(mcp.ListTotems()))

	if err := srv.Run(ctx, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "[ouija-mcp] %v\n", err)
		os.Exit(1)
	}
}
