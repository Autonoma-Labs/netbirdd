// Package autonoma exposes the Autonoma Environment Factory endpoint: one
// signed HTTP route that seeds a complete, isolated NetBird account before an
// end-to-end test run and removes it afterwards.
//
// Every model is created through the same manager the product itself calls, so
// the seeded data carries the real validation, events, IdP records and network
// map updates a hand-made INSERT would skip. See AGENTS.md ("Autonoma test
// data") for what to do when a model or its creation path changes.
package autonoma

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gorilla/mux"
	log "github.com/sirupsen/logrus"

	sdk "github.com/autonoma-ai/sdk/sdks/go/autonoma"

	"github.com/netbirdio/netbird/management/internals/modules/agentnetwork"
	agentNetworkTypes "github.com/netbirdio/netbird/management/internals/modules/agentnetwork/types"
	"github.com/netbirdio/netbird/management/internals/modules/reverseproxy/accesslogs"
	domainmanager "github.com/netbirdio/netbird/management/internals/modules/reverseproxy/domain/manager"
	"github.com/netbirdio/netbird/management/internals/modules/reverseproxy/proxy"
	"github.com/netbirdio/netbird/management/internals/modules/reverseproxy/service"
	"github.com/netbirdio/netbird/management/internals/modules/zones"
	"github.com/netbirdio/netbird/management/internals/modules/zones/records"
	"github.com/netbirdio/netbird/management/server/account"
	"github.com/netbirdio/netbird/management/server/http/middleware/bypass"
	"github.com/netbirdio/netbird/management/server/idp"
	"github.com/netbirdio/netbird/management/server/networks"
	"github.com/netbirdio/netbird/management/server/networks/resources"
	"github.com/netbirdio/netbird/management/server/networks/routers"
	"github.com/netbirdio/netbird/management/server/store"
)

const (
	// EndpointPath is the route the handler is mounted on, relative to the
	// /api prefix the management router already carries.
	EndpointPath = "/autonoma"

	// sharedSecretEnv is signed by Autonoma and verified here.
	sharedSecretEnv = "AUTONOMA_SHARED_SECRET" //nolint:gosec // an env var name, not a secret
	// signingSecretEnv never leaves the server; it signs the teardown token.
	signingSecretEnv = "AUTONOMA_SIGNING_SECRET"

	// scopeField names the column every seeded model hangs off, so the
	// dashboard knows how test data is isolated.
	scopeField = "accountId"
)

// Deps carries the managers the factories create data through. Everything here
// is the production instance; the endpoint owns no storage of its own.
type Deps struct {
	AccountManager      account.Manager
	IdpManager          idp.Manager
	Store               store.Store
	NetworksManager     networks.Manager
	ResourcesManager    resources.Manager
	RoutersManager      routers.Manager
	ZonesManager        zones.Manager
	RecordsManager      records.Manager
	ServiceManager      service.Manager
	DomainManager       *domainmanager.Manager
	AccessLogsManager   accesslogs.Manager
	ProxyManager        proxy.Manager
	AgentNetworkManager agentnetwork.Manager
}

// cleaner is the narrow set of scoped deletes the teardown needs for tables an
// account delete does not cascade into. Implemented by *store.SqlStore.
type cleaner interface {
	DeletePeerJobForTestData(ctx context.Context, accountID, jobID string) error
	DeleteProxyAccessTokenForTestData(ctx context.Context, accountID, tokenID string) error
	DeleteAccessLogForTestData(ctx context.Context, accountID, logID string) error
	DeleteProxyForTestData(ctx context.Context, proxyID, sessionID string) error
	DeleteAgentNetworkConsumptionForTestData(ctx context.Context, accountID string, kind agentNetworkTypes.ConsumptionDimension, dimID string, windowSeconds int64, windowStart time.Time) error
}

// AddEndpoints mounts the Environment Factory endpoint when both Autonoma
// secrets are present in the environment. Absent secrets means the route is
// never registered at all: that is the production guard, and it is plain code
// here rather than a flag inside the SDK so it is obvious when the endpoint
// exists. HMAC verification of every request is the second gate, and the SDK
// applies it on our behalf.
func AddEndpoints(deps Deps, router *mux.Router) error {
	sharedSecret := os.Getenv(sharedSecretEnv)
	signingSecret := os.Getenv(signingSecretEnv)
	if sharedSecret == "" || signingSecret == "" {
		log.Infof("autonoma: test-data endpoint disabled, %s and %s are not both set", sharedSecretEnv, signingSecretEnv)
		return nil
	}
	if sharedSecret == signingSecret {
		return fmt.Errorf("autonoma: %s and %s must be different values", sharedSecretEnv, signingSecretEnv)
	}
	if deps.AccountManager == nil {
		return fmt.Errorf("autonoma: account manager is required")
	}

	f := &factories{deps: deps}
	if c, ok := deps.Store.(cleaner); ok {
		f.cleaner = c
	} else {
		return fmt.Errorf("autonoma: store does not support scoped test-data teardown")
	}

	config := &sdk.HandlerConfig{
		ScopeField:    scopeField,
		SharedSecret:  sharedSecret,
		SigningSecret: signingSecret,
		SDK:           &sdk.SdkInfo{Orm: "gorm", Server: "gorilla-mux"},
		Factories:     f.registry(),
		Auth:          f.auth,
	}

	// The endpoint authenticates itself with the HMAC signature the SDK
	// verifies, so it must not go through the JWT/PAT middleware.
	if err := bypass.AddBypassPath("/api" + EndpointPath); err != nil {
		return fmt.Errorf("autonoma: add bypass path: %w", err)
	}

	router.HandleFunc(EndpointPath, handle(config)).Methods("POST", "OPTIONS")
	log.Infof("autonoma: test-data endpoint registered on /api%s with %d factories", EndpointPath, len(config.Factories))

	return nil
}

// handle adapts the SDK's framework-agnostic entry point to net/http. The SDK
// ships a Gin adapter only, and the management API is gorilla/mux, so the glue
// lives here: read the raw body (the signature covers the exact bytes),
// lower-case the header names the SDK looks up, and write back what it returns.
func handle(config *sdk.HandlerConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"error": "failed to read request body",
				"code":  "INTERNAL_ERROR",
			})
			return
		}

		headers := make(map[string]string, len(r.Header))
		for name, values := range r.Header {
			if len(values) > 0 {
				headers[strings.ToLower(name)] = values[0]
			}
		}

		result := sdk.HandleRequest(config, sdk.HandlerRequest{Body: string(body), Headers: headers})
		if result.Status >= http.StatusBadRequest {
			log.WithContext(r.Context()).Warnf("autonoma: request failed with %d: %v", result.Status, result.Body["error"])
		}
		writeJSON(w, result.Status, result.Body)
	}
}
