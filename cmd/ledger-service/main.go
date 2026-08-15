package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Azizx1/ledger-service/internal/config"
	"github.com/Azizx1/ledger-service/internal/httpapi"
	"github.com/Azizx1/ledger-service/internal/ledger"
	"github.com/Azizx1/ledger-service/internal/observability"
	"github.com/Azizx1/ledger-service/internal/service"
)

func main() {
	logOutput := bufio.NewWriterSize(os.Stdout, 64<<10)
	logger := slog.New(slog.NewJSONHandler(logOutput, nil))
	runErr := run(logger)
	if runErr != nil {
		logger.Error("ledger service stopped", "error", runErr)
	}
	flushErr := logOutput.Flush()
	if flushErr != nil {
		fmt.Fprintln(os.Stderr, "flush logs:", flushErr)
	}
	if runErr != nil || flushErr != nil {
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	configuration, err := config.Load()
	if err != nil {
		return err
	}

	rootContext, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	ledgerClient, err := ledger.NewClient(configuration.TigerBeetleClusterID, configuration.TigerBeetleAddresses)
	if err != nil {
		return err
	}
	defer ledgerClient.Close()

	metrics := observability.NewMetrics()
	ledgerService := service.New(
		ledgerClient,
		configuration.LedgerID,
		configuration.AuthorizationTimeout,
		configuration.RiskEvaluationDelay,
		configuration.RiskAutoApproveLimitCents,
		logger,
		metrics,
	)
	if err := ledgerService.EnsureSystemAccounts(); err != nil {
		return err
	}

	server := &http.Server{
		Addr:              configuration.HTTPAddress,
		Handler:           httpapi.NewHandler(ledgerService, logger, metrics, configuration.MaxConcurrentRequests),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	errorChannel := make(chan error, 1)
	go func() {
		logger.Info("ledger service listening", "address", configuration.HTTPAddress, "ledger_id", configuration.LedgerID)
		errorChannel <- server.ListenAndServe()
	}()

	select {
	case <-rootContext.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			return err
		}
		return nil
	case err := <-errorChannel:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
