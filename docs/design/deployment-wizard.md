# Test-host deployment wizard

The root `deploy.sh` and `deploy.bat` launch the same Go program in `cmd/deploy`,
adapted from the card-capture deployment wizard. It deploys only the isolated
email-verification test host. The library and Composer integration remain usable
without this wizard. No admin/index component or public API is involved.

## First run and reruns

Install Go 1.25+ (infrastructure automatically selects Go 1.27.3+), AWS CLI v2,
Pulumi CLI v3 and an AWS IAM Identity Center profile. Run from any directory:

```sh
./deploy.sh
# Windows:
# deploy.bat
```

First setup asks for project identity, then environment, AWS profile/region,
state storage and passphrases. It discovers account/role through STS, logs into
SSO when needed and displays the target before proceeding. Saved answers are
reused. At a text prompt with a default, typing stops the five-second countdown
and lets you finish editing. Press Enter to submit or Escape to cancel. Pressing
Enter on an empty line selects the displayed default. Project/environment values are isolated; account mismatches stop the run.
The Pulumi project stays `email-verification-testhost`, with stacks `test-<environment>`.
Use dev, qa or a custom test environment; production environments are refused.

The wizard asks whether you have an S3 bucket for deployment state. The default
no answer creates a collision-safe encrypted, versioned bucket with public-access
blocking and TLS enforcement. An existing bucket is checked before use. Previously
configured storage is never silently replaced after an access failure.

Nonsecret records live under `~/.infra-composer/<project>/environments/<environment>/`.
Repository `.deploy/` holds identity/flag snapshots and an encrypted local vault.
The vault uses PBKDF2 and AES-GCM with random salt/nonce; its unlock password is
never saved. The Pulumi passphrase is passed through the process environment.
Keep backups of the vault, unlock password, state URL and Pulumi passphrase.
S3 encryption, Pulumi secret encryption and the local vault are distinct protections.

Required host settings are `--sender`, `--recipient` and `--domain`, or interactive
answers. They are remembered per environment. The domain is ownership metadata,
not a hosted endpoint. Verify the SES sender and, in sandbox accounts, the recipient
before using the [test client](test-host.md#run-an-email-verification).

## Actions

```sh
./deploy.sh --action build
./deploy.sh --action setup
./deploy.sh --action preview
./deploy.sh --action deploy
./deploy.sh --action status
./deploy.sh --action report
./deploy.sh --action report --refresh-report
./deploy.sh --reconfigure --action setup
```

Plain interactive invocation defaults to deploy: build, successful preview, then
“Deploy the previewed changes? [Y/n]”. Enter/yes applies immediately; five seconds
without input applies that saved plan. No/Escape/Ctrl+C/EOF cancels. Required text,
secret entry and destructive confirmations never receive timeout approval.
Deletion/replacement additionally requires typing the exact stack name. Explicit
setup/build/status/preview/report never become deploy. Build needs no AWS session.

Builds create a Linux ARM64 Lambda ZIP and cache input/artifact hashes under
`dist/testhost/`. Unchanged verified artifacts are reused; `--force-rebuild`
invalidates reuse. Every preview/apply still queries Pulumi. Encrypted stack config
is refreshed before updates, preserving generated secret values and keys.

Operation locks prevent concurrent same-environment runs. Interrupted bootstrap
and stack discovery retain receipts. A missing previously initialized stack or a
failed remote lookup stops for recovery. Do not remove a lock until you have
verified that its owner is no longer running. Restore state/vault backups rather
than deleting settings to bypass recovery checks. Rerun the same command after
resolving the cause. To adopt a stack created with the older helper, use its exact
S3 backend and environment; do not initialize a new backend over existing resources.

Preview/deployment attempts produce safe JSON, Markdown and HTML reports under
`.deploy/reports/`, including partial results. State inventory means recorded;
read-only refresh evidence has its own timestamp. Reports omit raw policies,
environment maps, state payloads and credentials.

```mermaid
flowchart TD
    I["Project and environment"] --> A["SSO and target verification"]
    A --> S["S3 state and encrypted vault"]
    S --> R["Discover stack and restore configuration"]
    R --> B["Build or reuse verified ZIP"]
    B --> P["Preview and save plan"]
    P --> D{"Deploy requested?"}
    D -->|"No"| O["Report only"]
    D -->|"Yes"| C["Confirm exact preview"]
    C --> U["Apply saved plan and report"]
```

## GitHub Actions

The manual `testhost-deploy.yml` workflow calls the same executable with explicit
inputs, OIDC and a concurrency group. It does not run on pushes. Create a protected
GitHub environment and an AWS OIDC deployment role scoped to that repository and
environment first. The wizard deliberately does not automatically create roles
or overwrite protection settings. The role needs the reviewed provisioning rights
for this test stack and its S3 state; the runtime role is separately least-privilege.

```sh
./deploy.sh --action github-setup --github-repository lambdawalker/go.apexfission.aws.email-verification --github-environment testhost-dev --github-role arn:aws:iam::ACCOUNT:role/ROLE
```

Authenticate `gh` with environment administration rights. Setup verifies the
repository/session, checks the existing environment, reconciles managed variables
and transfers `PULUMI_CONFIG_PASSPHRASE` via stdin. It preserves protections and
unrelated settings. Permission failures report incomplete setup. Secret delivery
cannot be read back; explicit setup redelivers the passphrase. Nothing dispatches
a deployment automatically.

Managed variables: `BENCH_PROJECT`, `BENCH_ACCOUNT`, `BENCH_REGION`, `BENCH_BACKEND`,
`BENCH_ENVIRONMENT`, `BENCH_ROLE_ARN`, `BENCH_SENDER`, `BENCH_RECIPIENT`, `BENCH_DOMAIN`.
These names retain compatibility with the source wizard. Select the matching
protected environment and target in Actions. Inputs must match its saved scope.
Noninteractive deploy requires explicit `--action deploy --approve`; omitted action
is preview. CI requires complete settings and never starts SSO or bootstraps state.

## Validation and remaining boundaries

Tests use fake commands for cached builds, artifact tampering, bootstrap recovery,
stack discovery, saved settings, scope mismatches, approval responses and safe
reports. Windows is cross-compiled; actual Windows console/ACL behavior still
needs an operator test. No real AWS/GitHub environment setup or deployment is run
merely to verify this tooling. The existing test-host limitations around scan-based
scheduling, pinned-key trust and safe cleanup still apply.
