# Autonoma test-data integration

Autonoma is an end-to-end testing service. Before it runs a suite against a
preview deployment it asks this repository's management API to seed a throwaway
account, and afterwards it asks for that account to be removed again. This
package is the endpoint it drives, and this file is its reference: what the
endpoint is, which manager each factory creates its model through, and where it
deliberately does not use one.

**SDK endpoint path: /api/autonoma** (the conventional default; nothing to
reconfigure on the Autonoma side.)

The endpoint mounts itself only when both `AUTONOMA_SHARED_SECRET` and
`AUTONOMA_SIGNING_SECRET` are present in the environment - absent secrets means
the route does not exist at all, which is the production guard. Every request is
HMAC-signed with the shared secret and verified by the SDK; the teardown token
`down` presents is signed with the signing secret, so `down` can only ever
delete what `up` created.

## Where the code lives

| Piece | File |
| --- | --- |
| Endpoint, secrets guard, net/http glue | `management/server/http/handlers/autonoma/handler.go` |
| Factory registry and the auth callback | `management/server/http/handlers/autonoma/factories.go` |
| Shared helpers (typed factories, ref lookups, time offsets) | `management/server/http/handlers/autonoma/support.go` |
| Tenancy and people factories | `management/server/http/handlers/autonoma/factories_account.go` |
| Overlay-network factories | `management/server/http/handlers/autonoma/factories_network.go` |
| Reverse-proxy factories | `management/server/http/handlers/autonoma/factories_proxy.go` |
| Agent-network factories | `management/server/http/handlers/autonoma/factories_agentnetwork.go` |
| Scoped deletes for tables an account delete does not cascade into | `management/server/store/sql_store_testdata.go` |
| Registration | `management/internals/server/boot.go` |
| Maintenance note for future changes | `AGENTS.md`, "Autonoma test data" |

Every factory creates its rows through the manager the REST API itself calls, so
the seeded data carries the real validation, activity events, embedded-IdP users
and network-map updates. The one exception is documented under
[Limitations](#limitations).

## Entities

Models the entity audit lists as `independently_created: true` get a factory.
Models it lists as dependents are minted by their owner's creation path and are
verified through that owner rather than seeded directly.

### Factories (from the entity audit)

- [x] SetupKey - `DefaultAccountManager.CreateSetupKey`
- [x] Peer - `DefaultAccountManager.AddPeer`
- [x] User - `DefaultAccountManager.CreateUser`, or `CreateUserInvite` +
      `AcceptUserInvite` when the recipe gives the member a password
- [x] PersonalAccessToken - `DefaultAccountManager.CreatePAT`
- [x] ProxyAccessToken - `types.CreateNewProxyAccessToken` + `Store.SaveProxyAccessToken`
- [x] Group - `DefaultAccountManager.CreateGroup`
- [x] Account - `EmbeddedIdPManager.CreateUserWithPassword` +
      `DefaultAccountManager.GetAccountIDByUserID`
- [x] Policy - `DefaultAccountManager.SavePolicy`
- [x] Route - `DefaultAccountManager.CreateRoute`
- [x] NameServerGroup - `DefaultAccountManager.CreateNameServerGroup`
- [x] installation - `Store.SaveInstallationID`
- [x] Checks - `DefaultAccountManager.SavePostureChecks`
- [x] NetworkRouter - `routers.Manager.CreateRouter`
- [x] NetworkResource - `resources.Manager.CreateResource`
- [x] Job - `types.NewJob` + `Store.CreatePeerJob` (see Limitations)
- [x] Zone - `zones.Manager.CreateZone`
- [x] Record - `records.Manager.CreateRecord`
- [x] UserInviteRecord - `DefaultAccountManager.CreateUserInvite`
- [x] Service - `service.Manager.CreateService`
- [x] Domain - `domain.Manager.CreateDomain`
- [x] AccessLogEntry - `accesslogs.Manager.SaveAccessLog`
- [x] Proxy - `proxy.Manager.Connect`
- [x] Provider - `agentnetwork.Manager.CreateProvider`
- [x] Guardrail - `agentnetwork.Manager.CreateGuardrail`
- [x] Consumption - `agentnetwork.Manager.RecordConsumption`
- [x] AccountBudgetRule - `agentnetwork.Manager.CreateBudgetRule`

### Factories added beyond the audit

- [x] **Network** - `networks.Manager.CreateNetwork`. The audit's `Network`
      entry describes the account's own IP range, which is created with the
      account and needs no factory. This is the separate `networks` table behind
      the Networks feature, and neither `NetworkRouter` nor `NetworkResource`
      can be created without one.
- [x] **AgentNetworkSettings** - `agentnetwork.Manager.CreateSettings`.
      Bootstraps the account's LLM gateway. Without it an agent-network request
      leaves usage rows but no access-log trail, so the `AgentNetworkAccessLog`
      dependent the audit lists never materialises.

### Dependents verified through their owner

- [x] GroupPeer - Group (`peers`) and Peer (default "All" membership)
- [x] PolicyRule - Policy (`rules`)
- [x] Settings, ExtraSettings, Network (account IP range), AccountOnboarding - Account
- [x] NetworkAddress - Peer (`networkAddresses`, stored in the peer's system metadata)
- [x] Target - Service (`targets`)
- [x] AgentNetworkAccessLog, AgentNetworkAccessLogGroup, AgentNetworkUsage,
      AgentNetworkUsageGroup - AccessLogEntry with `agentNetwork: true`

## Integration checklist

- [x] Endpoint mounted at `/api/autonoma`, HMAC-verified, auth middleware bypassed
- [x] Teardown scoped to the seeded account
- [x] Auth callback returns real credentials
- [x] Maintenance note in `AGENTS.md`
- [x] Full recipe seeds and tears down in one pass
- [x] Concurrent instances proof (`sdk up --repeat 3`)
- [x] `sdk check` clean on `recipe.json`
- [x] Branch pushed and pull request opened

## How it was validated

Against a local `combined` server (management + signal + relay + embedded Dex on
one port, SQLite store), driven through the planner CLI's signed client and
checked by querying the database and the app's own REST API after each step.

- Every entity seeded and torn down in dependency slices, with the rows read
  back out of SQLite each time.
- The full recipe up, then down: 41 records plus their dependents created, and
  every table back to zero afterwards.
- Unsigned, wrongly signed and tampered requests rejected with `401`; a forged
  teardown token rejected with `403`.
- The auth payload's Personal Access Token authenticates real API calls, and the
  owner's email and password sign in through the embedded IdP's actual login
  form, yielding an authorization code that exchanges for a working JWT.
- Three instances of the recipe live at once (`--repeat 3`), all three seeded and
  all three torn down.

### Time-sensitive values

Every field the product compares against the current time takes an **offset** in
the recipe and is derived when the row is written, so the same recipe still seeds
correct data months from now. Each was confirmed to land on the intended side of
now through the app's own query:

| Field | Recipe input | Checked with |
| --- | --- | --- |
| `SetupKey.ExpiresAt` | `expiresInHours` | `GET /api/setup-keys` reports `valid: true`, `state: "valid"` |
| `UserInviteRecord.ExpiresAt` | `expiresInSeconds` | `GET /api/users/invites` reports `expired: false` |
| `PersonalAccessToken.ExpirationDate` | `expiresInDays` | the token authenticates a live request |
| `ProxyAccessToken.ExpiresAt` | `expiresInHours` | `GET /api/reverse-proxies/proxy-tokens` shows a future expiry |
| `Peer.Status.LastSeen` | `lastSeenMinutesAgo` | `GET /api/peers` renders the intended online / offline split |
| `Job.CreatedAt` / `CompletedAt` | `createdMinutesAgo` | `GET /api/peers/{id}/jobs` |
| `AccessLogEntry.Timestamp` | `minutesAgo` | `GET /api/events/proxy` and `GET /api/agent-network/access-logs` with an explicit range |
| `Consumption.WindowStartUTC` | `windowSeconds` | `GET /api/agent-network/consumption` lands in the current window |
| `Proxy.LastSeen` | `heartbeatValidForMinutes` | `GET /api/reverse-proxies/clusters` reports `online: true` |

Values the product never compares against now - a peer's IP, a DNS record's
content, a budget's caps - stay concrete.

### Values that must be unique per run

Enumerated from the schema, the migrations and the live database's unique
indexes, then given a token so concurrent runs cannot collide:

| Constraint | Recipe field carrying the token |
| --- | --- |
| Dex user email (globally unique in the IdP) | `Account.ownerEmail`, `User.email`, `UserInviteRecord.email` |
| `domains.domain` (unique across all accounts) | `Domain.domain` |
| `services.domain` (unique across all accounts) | `Service` inherits it through `domainId` |
| `agent_network_settings.domain` (unique across all accounts) | `AgentNetworkSettings.endpoint` |
| proxy cluster address (unique per account) | `Proxy.clusterAddress` |
| `accounts.domain` (drives domain-primary account matching) | `Account.domain` |
| `zones.domain` and its records | `Zone.domain`, `Record.name`, `Record.content` |
| `peers.key`, `peers.ip`, `peers.dns_label` | generated per row by the factory, never supplied by the recipe |
| `proxy_access_tokens.hashed_token`, PAT and invite tokens | minted by the product |

## Limitations

- **`Job` is the one factory that cannot go through its manager.**
  `DefaultAccountManager.CreatePeerJob` refuses a peer with no live gRPC stream
  and pushes the job down that stream before persisting it; a seeded peer has no
  agent behind it. The factory therefore builds the job with the product's own
  `types.NewJob` constructor - so the workload is validated and renders in the
  API exactly as a real one does - and writes it with the same
  `Store.CreatePeerJob` call the manager's transaction makes. The only side
  effect skipped is the push to the agent, which has no agent to reach.
- **`installation` is a genuine global singleton.** The `installations` table
  holds one row on a fixed primary key (`SqlStore.SaveInstallationID` upserts
  it), so it cannot be made per-run. Seeding it overwrites the deployment's
  installation id and teardown restores the previous value; concurrent runs
  overwrite each other's value rather than colliding, and the last teardown puts
  the original back. This was verified with `--repeat 3`.
- **A seeded `Proxy` has no process sending heartbeats.** A proxy counts as
  active only while its last heartbeat is under two minutes old, which nothing
  in a recipe can keep true. The factory stamps the heartbeat
  `heartbeatValidForMinutes` ahead instead (default two hours), which keeps the
  cluster online for the length of a run - long enough for the domain and
  service flows that depend on it.
- **`GET /api/agent-network/usage/overview` returns an empty aggregate against a
  SQLite store** even though the underlying usage row is seeded correctly and is
  returned by `GET /api/agent-network/access-logs` and
  `/agent-network/access-log-sessions` for the same time range. This is existing
  behaviour of that endpoint, not stale test data.
