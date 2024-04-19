package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/diamondburned/twidiscord/service"
	"github.com/spf13/pflag"
	"github.com/twipi/twipi/twicmd/httpservice"
	"golang.org/x/sync/errgroup"
	"libdb.so/hserve"
)

var (
	sqlitePath = "/tmp/twidiscord.sqlite"
	listenAddr = ":8080"
)

const help = `
Usages:

  %[1]s [flags]
    Start the twidiscord server.

  %[1]s [flags] add-account <user_number> <server_number> <token>
    Add an account to the database.

Flags:

`

func init() {
	pflag.Usage = func() {
		arg0 := filepath.Base(os.Args[0])
		fmt.Fprintf(os.Stderr, strings.TrimLeft(help, "\n"), arg0)
		pflag.PrintDefaults()
		fmt.Fprintln(os.Stderr)
	}
	pflag.StringVarP(&sqlitePath, "sqlite-path", "p", sqlitePath, "path to the SQLite database")
	pflag.StringVarP(&listenAddr, "listen-addr", "l", listenAddr, "address to listen on")
	pflag.Parse()
}

func main() {
	logger := slog.Default()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	errg, ctx := errgroup.WithContext(ctx)

	svc := service.NewService(sqlitePath, logger)
	errg.Go(func() error { return svc.Start(ctx) })

	handler := httpservice.NewHTTPServer(svc, logger.With("component", "http"))
	errg.Go(func() error {
		<-ctx.Done()
		if err := handler.Close(); err != nil {
			logger.Error(
				"failed to close http service handler",
				"err", err)
		}
		return ctx.Err()
	})

	errg.Go(func() error {
		logger.Info(
			"listening via HTTP",
			"addr", listenAddr)

		if err := hserve.ListenAndServe(ctx, listenAddr, handler); err != nil {
			logger.Error(
				"failed to listen and serve",
				"err", err)
			return err
		}

		return ctx.Err()
	})

	if err := errg.Wait(); err != nil {
		logger.Error(
			"service error",
			"err", err)
		os.Exit(1)
	}
}
