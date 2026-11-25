# Deploy demo environment discovery

The directory is an optional shared Pulumi stack. For `apexfission.com`, its
public URL is
`https://index.components.email-verification.apexfission.com/v1/environments`.
It lists environment IDs, API origins, and public Cognito connection settings.

## Create the shared index

From the repository root, build the Linux ARM64 Lambda archive. Use the same
AWS account as the test-host deployment roles. Choose the shared Pulumi state
backend and AWS region. Run `pulumi stack init shared` once to create the
stack, then use `pulumi stack select shared` on later runs.

```sh
archive=$(./scripts/build-discovery.sh)
cd infrastructure/cmd/discovery

pulumi stack init shared
pulumi config set aws:region us-east-2
pulumi config set domain apexfission.com
pulumi config set dnsProvider cloudflare
pulumi config set dnsZoneId "$INDEX_DNS_ZONE_ID"
pulumi config set archive "$archive"

pulumi preview --save-plan index.plan
# After reviewing the plan
pulumi up --plan index.plan
pulumi stack output
```

For Cloudflare, set `CLOUDFLARE_API_TOKEN` for the selected zone.
For Route 53, set `dnsProvider` to `route53` and use the public hosted zone ID.
Keep the shared stack separate from `test-dev` and `test-qa`.

## Publish test hosts

The shared stack outputs `discoveryURL`, `discoveryRegion`, and
`discoveryExecutionArn`. Give each environment's deployment role
`execute-api:Invoke` for its write route only. For example, append
`/*/POST/v1/environments/dev/changes` to the execution ARN for the dev role.

For a local deployment, pass the URL and region through the following flags.
For GitHub deployments, set `BENCH_INDEX_URL` and `BENCH_INDEX_REGION` in the
protected deployment environment.

```sh
./deploy.sh --action deploy --environment dev \
  --discovery-index "$DISCOVERY_URL" --discovery-region "$DISCOVERY_REGION"
```

The wizard reserves a revision after approval, applies the saved plan, and
checks that the mapped HTTPS verification route answers with the authentication
challenge. It publishes only the declared public outputs after that check.
The challenge checks API reachability and authorization configuration. SES
verification and end-to-end email delivery use the existing test-host checks.

The deployment saves the index address and region in its local scope for
future deploys and destroy. A confirmed destroy retires the entry before
removing resources, so a partial destroy leaves the entry off the directory.
A successful deploy republishes it. Preview and setup leave the directory
untouched. If publication fails after a successful apply, fix the reported
issue and rerun deploy. The last published entry stays available until a
successful replacement or a confirmed retirement.

The **Remove this environment** action retires the selected environment's
entry and destroys its stack. **Remove All Environments and Index** in the
main menu first verifies the
existing shared S3 backend and `shared` discovery stack. Enter that index
backend URL and its Pulumi passphrase. The wizard requests confirmation for
each saved environment, retires its entry, and destroys its stack. After all
environment stacks are gone, it destroys the shared discovery index stack.
If a stack cannot be removed, the wizard stops before removing the index so
the remaining environments can be recovered and retried. GitHub deployment
settings can be removed per selected environment using the separate
**Remove GitHub configuration** card.

## Connect the Android demo

Open the live connection form and choose a published environment. The demo
fills in its API origin and shows the Cognito pool and app client for the
tester session. Use the Cognito access token for that environment. The
form also accepts the test-host URL manually.
