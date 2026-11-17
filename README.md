# go.apexfission.aws.email-verification

Go library for host-authorized email verification. The host owns eligibility,
accounts, sessions and the action authorized by verification evidence.

**Status: first executable core; not production-ready.** See
[implementation status and decisions](docs/implementation-status.md) for boundaries
and remaining AWS integration work. The [design overview](docs/verification-only-plan.md)
remains the target specification, not a claim that every feature is implemented.

## Run

Go 1.25 or newer:

```sh
go test -race ./...
go vet ./...
go run ./cmd/demo
bash scripts/preflight.sh config/examples/default.json
```

The demo performs an in-memory start, encrypted durable email job, confirmation,
ES256 issuance, host acceptance and acknowledgment. It sends no real email and
makes no AWS calls. It prints no proofs, tokens or assertions.

## Integration

Import the root package as `verification`. Create a `Host` and `Service` with
separate stores. Constructors validate the effective timing configuration.
`storage.Memory` is only for tests/demo. `adapters/dynamodb.Store` implements
strongly consistent reads and conditional aggregate writes; each table needs a
string partition key named `pk` and on-demand capacity.

Trusted host code calls `Prepare`, then `Dispatch` to obtain a one-use permit.
`Service.Start` consumes the permit and persists an encrypted email payload in
the same aggregate as proof state. Call `Host.BindTransaction` before accepting
results. Resume email delivery with `SendMail`; confirm with A plus B or C;
use `Issue` and `Deliver` for signing and the trusted in-process handoff.

The host supplies a `Mailer`, an authenticated authoritative `Trust` provider,
its clock, storage clients, a proof HMAC key and a separate email encryption key.
`adapters/kmssigner.Signer` supplies KMS signing. `LocalSigner` is for local tests.
Trusted configuration must authorize signing key identity and lifetime.

Methods handling `Request`, `Permit`, stores, signing or acceptance are backend
integration APIs, not endpoints that accept arbitrary client JSON. Host HTTP
handlers must enforce caller/session ownership and project only safe DTOs.
Do not expose a complete `Transaction` to a client: it contains internal workflow
state. Status APIs return a deliberately limited `Progress` value.

## Deployment gate

```sh
bash scripts/preflight.sh effective-config.json your-deployment-command args
```

The command is invoked only after validation succeeds. JSON values are integer
seconds and overlay documented defaults; unknown, duplicate and null fields are
rejected. Environment overrides must be materialized into that effective JSON
before this gate. No cloud deployment command or AWS resources are created here.

The pipeline runs tests, race detection, vet and default configuration validation.
Host deployments must use the gate and the same constructor/startup validation.
