package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/spf13/pflag"
	"github.com/twipi/twidiscord/service"
	"github.com/twipi/twidiscord/store"
	"github.com/twipi/twidiscord/store/sqlite"
	twicmdhttp "github.com/twipi/twipi/twicmd/http"
	"github.com/twipi/twipi/twisms"
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
	switch pflag.Arg(0) {
	case "add-account", "":
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()

		slog.SetLogLoggerLevel(slog.LevelDebug)
		logger := slog.Default()

		db, err := sqlite.New(ctx, sqlitePath)
		if err != nil {
			logger.Error(
				"failed to open SQLite database",
				"sqlite_path", sqlitePath,
				"err", err)
			os.Exit(1)
		}
		defer db.Close()

		var status int
		switch pflag.Arg(0) {
		case "add-account":
			status = addAccount(ctx, db, logger, pflag.Args()[1:]...)
		case "":
			status = start(ctx, db, logger)
		}

		db.Close()
		os.Exit(status)

	default:
		pflag.Usage()
		os.Exit(1)
	}
}

func addAccount(ctx context.Context, db store.Store, logger *slog.Logger, args ...string) int {
	if len(args) != 3 {
		pflag.Usage()
		return 1
	}

	account := store.Account{
		UserNumber:   args[0],
		ServerNumber: args[1],
		DiscordToken: args[2],
	}

	if err := twisms.ValidatePhoneNumber(account.UserNumber); err != nil {
		logger.Error(
			"invalid user number",
			"user_number", account.UserNumber,
			"err", err)
		return 1
	}

	if err := twisms.ValidatePhoneNumber(account.ServerNumber); err != nil {
		logger.Error(
			"invalid server number",
			"server_number", account.ServerNumber,
			"err", err)
		return 1
	}

	if err := db.SetAccount(ctx, account); err != nil {
		logger.Error(
			"failed to add account to database",
			"account", account,
			"err", err)
		return 1
	}

	logger.Info("added account to database")
	return 0
}

func start(ctx context.Context, db store.Store, logger *slog.Logger) int {
	errg, ctx := errgroup.WithContext(ctx)

	svc := service.NewService(db, logger)
	errg.Go(func() error { return svc.Start(ctx) })

	handler := twicmdhttp.NewHandler(svc, logger.With("component", "http"))
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
		r := http.NewServeMux()
		r.Handle("GET /health", http.HandlerFunc(healthCheck))
		r.Handle("/", handler)

		logger.Info(
			"listening via HTTP",
			"addr", listenAddr)

		if err := hserve.ListenAndServe(ctx, listenAddr, r); err != nil {
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
		return 1
	}

	return 0
}

func healthCheck(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}
