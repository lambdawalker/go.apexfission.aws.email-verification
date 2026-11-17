# Implementation decisions

[Design overview](../verification-only-plan.md)

## Next implementation decisions

- Finalize the remaining choices in [Assertion schema, signing, and trusted keys](../design/assertions.md#assertion-schema-signing-and-trusted-keys).
  The initial fields, signed JWT/JWS format, AWS KMS approach, and proposed JWKS
  route, ES256 selection, and operational timing defaults are recorded there.
- Define direct-call and separate-Lambda contracts, including authorized initiation,
  result delivery, recovery and client completion behavior.
- Move eligibility, Cognito enrollment and grants/session code to the host side;
  remove their permissions and integration requirements from the verifier.
- Add shared assertion validation and host acceptance contracts; keep signing
  keys separate from the existing proof HMAC key.
- Test replay races, wrong audience/email/nonce, expired results, stolen B without
  A, resend invalidation, and crashes before and after acceptance.
- Review applicable standards before claiming conformance. PKCE-style binding,
  signed assertions and webhook-style deduplication are design inspirations;
  no NIST compliance or EVP interoperability is claimed.

Keep proofs and signed assertions out of logs and result URLs. Deliver results
through a direct trusted return when composed, or an authenticated backend
handoff when separated. No AWS deployment, proof-key rotation or data migration
is part of this documentation change.


