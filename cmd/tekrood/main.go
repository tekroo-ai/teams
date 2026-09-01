package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tekroo-ai/teams/adapters/httpapi"
	"github.com/tekroo-ai/teams/adapters/mcp"
	"github.com/tekroo-ai/teams/adapters/operationalruntime"
	"github.com/tekroo-ai/teams/adapters/operatorhttp"
	"github.com/tekroo-ai/teams/adapters/operatortools"
	"github.com/tekroo-ai/teams/adapters/protocol"
	"github.com/tekroo-ai/teams/kernel"
)

const (
	startupTimeout  = 20 * time.Second
	shutdownTimeout = 20 * time.Second
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "tekrood:", err)
		os.Exit(1)
	}
}

func run(arguments []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("tekrood", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "absolute path to tekrood JSON configuration")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 || *configPath == "" {
		return errors.New("usage: tekrood -config /absolute/path/tekrood.json")
	}
	config, err := operationalruntime.LoadProductionConfig(*configPath)
	if err != nil {
		return err
	}
	startupContext, startupCancel := context.WithTimeout(context.Background(), startupTimeout)
	listener, err := (&net.ListenConfig{}).Listen(startupContext, "tcp", config.Operator.Address)
	if err != nil {
		startupCancel()
		return fmt.Errorf("reserve operator endpoint: %w", err)
	}
	defer listener.Close()
	service, err := operationalruntime.NewProductionService(startupContext, config)
	startupCancel()
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if closed {
			return
		}
		closeContext, closeCancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer closeCancel()
		_ = service.Close(closeContext)
	}()
	startContext, startCancel := context.WithTimeout(context.Background(), startupTimeout)
	err = service.Start(startContext)
	startCancel()
	if err != nil {
		return err
	}
	stopRequested := make(chan struct{}, 1)
	token, operationTimeout, maximumBodyBytes := service.OperatorCredentials()
	operatorHandler, err := operatorhttp.NewHandler(operatorhttp.Config{Service: service, BearerToken: token, OperatorPrincipal: service.OperatorIdentity().Principal, OperationTimeout: operationTimeout, MaximumBodyBytes: maximumBodyBytes, RequestStop: func() {
		select {
		case stopRequested <- struct{}{}:
		default:
		}
	}})
	if err != nil {
		return err
	}
	gateway, err := protocol.NewGateway(service.Runtime, operationTimeout)
	if err != nil {
		return err
	}
	credentials := service.HumanTransportCredentials()
	bearerBindings := make([]httpapi.BearerBinding, 0, len(credentials)+1)
	bearerBindings = append(bearerBindings, httpapi.BearerBinding{Token: token, Identity: service.OperatorIdentity()})
	for _, credential := range credentials {
		bearerBindings = append(bearerBindings, httpapi.BearerBinding{Token: credential.Token, Identity: protocol.AuthenticatedContext{Principal: credential.Principal}})
	}
	authenticator, err := httpapi.NewMultiStaticBearerAuthenticator(bearerBindings)
	if err != nil {
		return err
	}
	focusedTools, err := operatortools.New(service)
	if err != nil {
		return err
	}
	mcpHandler, err := mcp.NewFocusedHandler(
		gateway,
		focusedTools,
		authenticator,
		httpapi.OriginPolicyFunc(func(string) bool { return false }),
		httpapi.RateLimiterFunc(func(protocol.AuthenticatedContext) bool { return true }),
		maximumBodyBytes,
	)
	if err != nil {
		return err
	}
	router := http.NewServeMux()
	router.Handle("/mcp", mcpHandler)
	router.Handle("/", operatorHandler)
	server := &http.Server{Addr: config.Operator.Address, Handler: router, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: time.Minute}
	serverResult := make(chan error, 1)
	go func() { serverResult <- server.Serve(listener) }()
	writeLog(stdout, serviceLog{Event: "tekrood_started", At: time.Now().UTC(), Contract: kernel.ContractIdentity, Database: config.Mongo.Database, OpenHands: config.OpenHands.BaseURL, Operator: config.Operator.Address, Workspaces: len(config.Workspaces), Profiles: len(config.Profiles)})

	shutdownSignal, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	var runtimeErr error
	select {
	case runtimeErr = <-service.Failures():
		writeLog(stderr, serviceLog{Event: "tekrood_failed", At: time.Now().UTC(), Error: runtimeErr.Error()})
	case serveErr := <-serverResult:
		if !errors.Is(serveErr, http.ErrServerClosed) {
			runtimeErr = serveErr
			writeLog(stderr, serviceLog{Event: "tekrood_failed", At: time.Now().UTC(), Error: serveErr.Error()})
		}
	case <-stopRequested:
	case <-shutdownSignal.Done():
	}

	serverContext, serverCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	serverErr := server.Shutdown(serverContext)
	serverCancel()
	stopContext, stopCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	err = service.Stop(stopContext)
	stopCancel()
	if err != nil || serverErr != nil {
		return errors.Join(runtimeErr, serverErr, err)
	}
	closeContext, closeCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	err = service.Close(closeContext)
	closeCancel()
	if err != nil {
		return err
	}
	closed = true
	writeLog(stdout, serviceLog{Event: "tekrood_stopped", At: time.Now().UTC(), Contract: kernel.ContractIdentity, Database: config.Mongo.Database})
	return runtimeErr
}

type serviceLog struct {
	Event      string    `json:"event"`
	At         time.Time `json:"at"`
	Contract   string    `json:"contract,omitempty"`
	Database   string    `json:"database,omitempty"`
	OpenHands  string    `json:"openhands,omitempty"`
	Operator   string    `json:"operator,omitempty"`
	Workspaces int       `json:"workspaces,omitempty"`
	Profiles   int       `json:"profiles,omitempty"`
	Error      string    `json:"error,omitempty"`
}

func writeLog(destination io.Writer, record serviceLog) {
	_ = json.NewEncoder(destination).Encode(record)
}
