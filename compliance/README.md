<!--
SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
SPDX-License-Identifier: Apache-2.0
-->

# Compliance suite

Black-box tests of what a caller of the WebPA API sees. They run the real
tr1d1um binary against fakes of everything it talks to, so they can run on
any machine with `go`, need no credentials, and finish in about fifteen
seconds.

```bash
go test ./compliance/          # as part of go test ./...
go test -race ./compliance/    # builds tr1d1um with the race detector too
go test ./compliance/ -run TestCapabilityMatrix -v
```

The suite is the executable form of the API contract: the response codes
in [README.md](../README.md), the capability rules in
[docs/capabilities.md](../docs/capabilities.md), and the webhook, WRP and
validation behavior the WebPA API has always had. A test here is a promise
to callers; change one only when the promise changes.

## How it works

`TestMain` (harness_test.go) builds tr1d1um once into a temporary
directory and starts three fakes:

- **The fake XMiDT** (fakes_test.go) stands in for scytale. It accepts the
  msgpack WRP tr1d1um POSTs to `/api/v3/device` and the stat GETs, answers
  for the devices a test has connected with `xmidt.connect`, and 404s for
  every other device. Every WRP a device receives is recorded, so a test
  can check exactly what tr1d1um sent: the command, the envelope, the
  partner IDs, the trace headers.
- **The fake Argus** stands in for the webhook store: an in-memory bucket
  behind the three store routes ancla's client uses, with TTL expiry.
- **The token signer** (tokens_test.go) is an RSA key generated at start.
  Its public half is written into tr1d1um's configuration as a fixed
  clortho key, so `signer.mint` produces tokens tr1d1um trusts, with
  whatever subject, partners, capabilities and timestamps a test wants.

Each test asks for a tr1d1um instance by **profile** with `appFor(t,
profile)`. A profile is `tr1d1um.yaml` plus overrides, mostly the mode of
each capability kind. An instance starts on first use, on free loopback
ports, and is stopped when the suite ends; all instances share the fakes.
The profiles are listed in harness_test.go: `enforce` is the one most
tests use.

`tr1d1um.yaml` is the deployment the suite describes. Read it to see what
the tests assume: the capability prefixes, the overrides keyed by
principal (a test opts into an override by minting a token with that
subject), the webhook validation limits, the short XMiDT timeout.

## Adding a test

1. Pick the file by subject: `auth_test.go` for authentication,
   `capability_matrix_test.go` for endpoint capabilities,
   `capabilities_test.go` for the narrowing kinds, `translation_test.go`
   for WDMP and WRP, `webhook_test.go`, `validation_test.go`,
   `operational_test.go`.
2. Get an instance with `appFor(t, enforce)`, a token with `allToken(t)`
   or `signer.mint(t, claims{...})`, and, for a device request, a device
   with `xmidt.connect(t, okReply)`.
3. Send with `send(t, request{...})` or `get(t, url, token)` and assert on
   the `apiResponse`. For a device request, `dev.next(t)` is the WRP the
   device received and `.payload(t)` its JSON command.
4. For a metric, `counterDelta(t, a, checkMetric, labels, func() {...})`
   is how much the counter grew while the function ran.

Keep a test to one promise, name it for the promise, and never make it
depend on another test's registrations: mint a fresh token, connect a fresh
device, use `uniqueListenerURL()` for a webhook.

## What is deliberately not here

Deployment properties the internal acceptance suite checked against live
clusters have no meaning against a binary on loopback and were left out:
TLS certificates, dual-stack listening, region and stage headers, the
fan-out count across regions, and event delivery to a listener (that is
caduceus's job; the suite checks that a registration reaches the store).
