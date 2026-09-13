package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tekroo-ai/teams/adapters/eventexporthttp"
	"github.com/tekroo-ai/teams/adapters/httpapi"
	"github.com/tekroo-ai/teams/adapters/mongo"
	"github.com/tekroo-ai/teams/adapters/protocol"
	"github.com/tekroo-ai/teams/eventexport"
	"github.com/tekroo-ai/teams/kernel"
)

const kernelManifestSHA256 = kernel.Digest("85306c8edc703e85df502280642ef30161e16b8e82ff48f568c5a5c6d421f12d")

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "event-export-server:", err)
		os.Exit(1)
	}
}

func run() error {
	configuration, err := loadConfiguration()
	if err != nil {
		return err
	}
	startupContext, startupCancel := context.WithTimeout(context.Background(), 15*time.Second)
	source, err := mongo.OpenReadOnlyEventExportSource(startupContext, mongo.ReadOnlyEventExportConfig{URI: configuration.mongoURI, Database: configuration.database, SourceID: configuration.sourceID, ContractIdentity: kernel.ContractIdentity, ManifestSHA256: kernelManifestSHA256, MigrationLevel: 1, BacklogLimit: 10_000})
	startupCancel()
	if err != nil {
		return err
	}
	defer func() {
		closeContext, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()
		_ = source.Close(closeContext)
	}()
	identity := protocol.AuthenticatedContext{Principal: kernel.PrincipalRef{Kind: kernel.PrincipalService, ID: configuration.principalID}}
	authenticator, err := eventexporthttp.NewBearerAuthenticator(configuration.bearerToken, identity)
	if err != nil {
		return err
	}
	limiter, err := eventexporthttp.NewFixedWindowRateLimiter(120, time.Minute, nil)
	if err != nil {
		return err
	}
	service, err := eventexport.NewService(eventexport.Config{Source: source, Authorizer: eventexport.AuthorizerFunc(func(observed protocol.AuthenticatedContext, sourceID string) bool {
		return observed.Equal(identity) && sourceID == configuration.sourceID
	}), CursorKey: configuration.cursorKey, CursorTTL: 24 * time.Hour, MaximumLimit: eventexport.MaximumLimit, MaximumWait: eventexport.MaximumWait})
	if err != nil {
		return err
	}
	handler, err := eventexporthttp.NewEventExportHandler(service, authenticator, httpapi.OriginPolicyFunc(func(string) bool { return false }), limiter, eventexporthttp.DefaultEventExportMaxBodyBytes)
	if err != nil {
		return err
	}
	server := &http.Server{Addr: configuration.address, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 35 * time.Second, WriteTimeout: 35 * time.Second, IdleTimeout: time.Minute}
	shutdownContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	completed := make(chan error, 1)
	go func() { completed <- server.ListenAndServe() }()
	fmt.Printf("event-export-server ready address=%s source=%s contract=%s\n", configuration.address, configuration.sourceID, eventexport.ContractIdentity)
	select {
	case serveErr := <-completed:
		if errors.Is(serveErr, http.ErrServerClosed) {
			return nil
		}
		return serveErr
	case <-shutdownContext.Done():
		bounded, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(bounded); err != nil {
			return err
		}
		serveErr := <-completed
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return serveErr
		}
		fmt.Println("event-export-server stopped")
		return nil
	}
}

type configuration struct {
	address     string
	mongoURI    string
	database    string
	sourceID    string
	principalID string
	bearerToken string
	cursorKey   []byte
}

func loadConfiguration() (configuration, error) {
	value := configuration{address: environment("TEKROO_EVENT_EXPORT_ADDRESS", "127.0.0.1:8087"), mongoURI: os.Getenv("TEKROO_EVENT_EXPORT_MONGO_URI"), database: os.Getenv("TEKROO_EVENT_EXPORT_MONGO_DATABASE"), sourceID: environment("TEKROO_EVENT_EXPORT_SOURCE_ID", "teams-main"), principalID: environment("TEKROO_EVENT_EXPORT_PRINCIPAL_ID", "sma-event-export"), bearerToken: os.Getenv("TEKROO_EVENT_EXPORT_BEARER_TOKEN")}
	host, _, err := net.SplitHostPort(value.address)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return configuration{}, errors.New("TEKROO_EVENT_EXPORT_ADDRESS must be an explicit loopback IP and port")
	}
	encodedKey := os.Getenv("TEKROO_EVENT_EXPORT_CURSOR_KEY_HEX")
	value.cursorKey, err = hex.DecodeString(encodedKey)
	if err != nil || len(value.cursorKey) < 32 || value.mongoURI == "" || value.database == "" || value.sourceID == "" || value.principalID == "" || len(value.bearerToken) < 32 {
		return configuration{}, errors.New("Mongo URI/database and at least 32-byte bearer and cursor keys are required")
	}
	return value, nil
}

func environment(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
