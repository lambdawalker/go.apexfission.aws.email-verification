# go.apexfission.aws.email-verification

Planned Go/AWS module that verifies mailbox access for one host-authorized request and returns a signed verification assertion. The host owns eligibility checks, accounts, Cognito integration, and session tokens.

**Status: design only; implementation has not started.**

See the [design plan and diagrams](docs/verification-only-plan.md) for:

- Host and verifier responsibilities.
- A/B/C proof flow and signed-result delivery.
- Single-use acceptance, replay prevention, and crash recovery.

The host generates a random nonce tied to its pending operation; the verifier adds its own transaction ID. Both are covered by the signed result. The host validates the result and consumes the operation once.

This repository is the planned successor for the verification-only responsibilities currently implemented in [go.apexfission.aws.verifyemail](https://github.com/lambdawalker/go.apexfission.aws.verifyemail). Signing format, key management, and delivery contracts remain open design decisions.
