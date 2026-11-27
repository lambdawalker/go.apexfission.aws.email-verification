# Reusable AWS verification feature

The feature supplies verification runtime and infrastructure. Other projects
import public packages; they do not copy `internal/testhost` or its Pulumi factory.

## Ownership

| Component | Feature owns | Application supplies |
| --- | --- | --- |
| Core | Transactions, proof checks, durable mail/sign/delivery attempts, acknowledgment | Eligibility, operation authorization, host storage, acceptance and business work |
| AWS runtime | DynamoDB/Secrets Manager initialization, SES adapter, KMS signer | AWS client configuration, sender, message renderer, host ID and issuer |
| Worker | Bounded processing, durable cursor, Lambda event handler | Host acceptance adapter and authoritative trust policy |
| Infrastructure | Verifier table, KMS key, secret generation/storage, SES identity, worker Lambda/role/logs/schedule and API grants | Worker archive, host configuration and host storage permissions |
| Application API | Library methods and safe progress types | Authentication, routes, domain, API Lambda and user ownership checks |

`feature.Declare` / `infrastructure.Deploy` retain the version-1 storage/signing
contract. Existing storage-only consumers are unchanged. `NewRuntime` is an
additive native Pulumi component, used with the resolved contract outputs.

## Runtime integration

The API creates its own `verification.Host` with application-owned storage. Load
`awsruntime.Settings` explicitly or with `awsruntime.FromEnvironment(os.Getenv)`,
then call `awsruntime.NewAWS`. Initialization retrieves the proof secret and builds
the transaction store/service and protector; it does not call KMS or SES.

Keep the trusted initiation flow: authorize the operation, `Host.Prepare`,
`Host.Dispatch`, `Service.Start`, and `Host.BindTransaction`. Project safe DTOs to
clients. Confirmation and status use the same public library.

A separate worker process creates its own runtime instance. Its application-owned
host store and trust loader are explicit. Inside a worker initialization function:

```go
runtime, err := awsruntime.NewAWS(ctx, settings, awsConfig)
if err != nil { return err }
host, err := verification.NewHost(hostStore, settings.Policy, time.Now)
if err != nil { return err }
w, err := runtime.NewWorker(awsConfig, awsruntime.WorkerSettings{
    HostID: hostID, Issuer: issuer,
    KeyARN: keyARN, KeyID: keyID, Sender: sender,
    Deliver: worker.InProcess(runtime.Service, host, issuer, trustLoader),
})
if err != nil { return err }
lambda.Start(w.Handle)
return nil
```

Acceptance executes inside the worker process. Host acceptance and acknowledgment
are durable before the verifier considers delivery complete. There is no public
verifier endpoint or network callback. Host business work remains application-owned.

`sesmailer.Mailer` supports a renderer returning subject, text and HTML; the
default sends only the code. `AllowedRecipients` is an optional test restriction;
an empty list never bypasses host authorization. `kmstrust.Load` validates one
explicitly trusted KMS key. The demo selects it; production consumers must choose
a trust policy appropriate for their rotation/revocation requirements.

## Infrastructure integration

Call `infrastructure.NewRuntime` in the verification ownership scope with:

- `Backend`: native table name/ARN and signing key ID/ARN outputs.
- `Policy`: the same effective policy as the storage/signing declaration.
- `WorkerCode`: a Linux ARM64 ZIP containing the application's small worker entry
  point and acceptance adapter.
- `WorkerEnvironment`: application-specific host table, identity and configuration.
  Reserved verification bindings cannot be overridden.
- `HostPolicy`: scoped permissions needed by the acceptance adapter, if any.
- `Sender`, with optional `SenderARN` and `SecretARN` to reuse existing resources.
- `AllowedRecipients`: optional additional SES IAM recipient restriction.

The component never manages the lifecycle of supplied identities/secrets. Existing
secrets must contain independent base64-encoded 32-byte `hmac` and `encryption`
keys. Do not replace them while pending work still needs them.

Apply the returned `APIEnvironment` to the API Lambda and `GrantAPI` to its role.
This grants transaction Get/Put and secret retrieval only. Host-table and logging
permissions are separate. Worker grants add scanning, KMS signing/public-key access
and sender-scoped SES. Custom-encrypted supplied secrets may require an additional
scoped decrypt policy on the returned role. Parent/provider options are supported.

The demo builds one ZIP and deploys **two functions** with distinct roles, logs,
timeouts and environment variables. Fixed `VERIFICATION_MODE` selects the entry
point at startup; requests cannot select worker privileges. Other consumers may
provide a worker-only binary.

```sh
go run ./examples/consumer
bash scripts/testhost-import-test.sh
```

The script copies the example into a new external Go module and executes a full
start → email → confirm → sign → accept flow using only public packages. It also
checks that importing demo internals remains forbidden.

## Existing demo migration

The API function, routes, Cognito, hostname, host/verifier tables and signing key
retain their identities. Mobile request formats are unchanged. The API role loses
SES, KMS and scan privileges; EventBridge targets the new worker. Generated HMAC
and encryption values, secret container/version, sender, schedule and target have
aliases to their former names and parent, preserving existing encrypted mail.

Review the saved preview before applying: tables, signing key and secret/key
material must not be replaced. New worker compute/role/logs and updated grants and
target are expected. Mock tests check aliases; an actual stack preview is the
migration authority. Do not remove protection to force secret replacement.

The old host-table scan cursor becomes unused. The new cursor lives under a
reserved prefix in verifier storage; restarting pagination does not reset retries.
`testHostFunction` remains the API. Manual `tick` calls now use
`testHostWorkerFunction`; operators need invoke permission on that function.
`testHostWorkerLogs` and `testHostWorkerFunctionArn` expose its logs and ARN.

## Operational limits

Reuse is not a claim of complete production readiness. The default DynamoDB source
is a bounded scan for low-volume installations. It skips cursor metadata and
checkpoints pages across invocations. Large workloads should supply an indexed
`worker.Source` and validate throughput. Service CAS and stored retry deadlines
arbitrate concurrent workers; an ambiguous email response can still duplicate mail.
Unresolved records remain retained. This change adds no destructive TTL or automatic
key rotation. Archive/revocation, reconciliation and safe deletion work remains in
[implementation status](../implementation-status.md).
