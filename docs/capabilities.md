<!--
SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
SPDX-License-Identifier: Apache-2.0
-->

# Capabilities

A caller's JWT carries a list of capability strings. tr1d1um reads them to
decide which endpoints the caller may call, from which networks, how fast,
and which TR-181 parameters they may touch.

Read [How capabilities work](#how-capabilities-work) first, then the part
written for you:

- [Reference](#reference) describes each kind of capability: how it is
  written and how it is matched. Everyone needs this.
- [For operators](#for-operators) covers rolling a check out, the
  configuration knobs, and what to watch.
- [For callers](#for-callers) explains a refusal.
- [For token issuers](#for-token-issuers) is a checklist for minting tokens.
- [Worked example](#worked-example) walks one token through several calls.

Every setting named here lives under `auth.inbound.jwt` in
[tr1d1um.yaml](../tr1d1um.yaml), which documents each one in full.

## How capabilities work

There are four kinds of capability. Each kind starts with a prefix the
deployment configures for it, and nothing is built in. The examples in this
document use the prefixes from the sample configuration; your deployment's
prefixes will differ, so ask the operator.

| Kind      | Example                                 | Decides                  |
|-----------|-----------------------------------------|--------------------------|
| Endpoint  | `webpa-prefix-api:device/.*/config:get` | Which API calls?         |
| CIDR      | `webpa-prefix-cidr:96.44.169.0/24`      | From where?              |
| Rate      | `webpa-prefix-rate:3000/1m`             | How fast?                |
| Parameter | `webpa-prefix-param:Device.WiFi.**:read` | Which TR-181 parameters? |

A capability whose prefix the deployment did not configure is ignored. It
may belong to another service.

The prefix is an arbitrary string, matched literally, and the colons in the
examples are a convention, not syntax. tr1d1um cuts the configured prefix
off the front of each capability and parses what remains. These four
configurations are equally valid, and the capability is whatever follows:

```text
webpa-prefix-cidr:96.44.169.0/24
webpa:cidr:96.44.169.0/24
webpa-prefix-cidr|96.44.169.0/24
foo-bar-apple-cider96.44.169.0/24
```

The colons that do matter are the ones after the prefix. Endpoint and
parameter capabilities use `:` to separate the method or operation from
what precedes it, so `device/.*/config:get` and `Device.WiFi.**:read` are
read the same way whatever prefix was cut off.

One constraint: no kind's prefix should be a prefix of another's. Each kind
cuts its own prefix on its own, so with `webpa-prefix-` for endpoints and
`webpa-prefix-cidr:` for CIDR, every CIDR capability would also be parsed
as an endpoint capability. It would grant nothing, since no path matches,
but it would show up as noise. Give each kind a distinct name and a
terminator, as the sample configuration does.

Four rules apply to every kind:

1. **Endpoint capabilities grant. The other three only narrow.** A token
   with no capability of a narrowing kind is unrestricted on that kind. It
   may call from anywhere, at any rate, on any parameter its endpoint
   capabilities allow. Operators can change this for CIDR with a `required`
   mode and for rate with `defaults`; see [For operators](#for-operators).
2. **Each kind is checked on its own.** A request must pass every kind the
   token carries. There is no way to tie a CIDR to a rate, or a rate to a
   parameter.
3. **A malformed capability still counts, but matches nothing.** A typo
   never removes a restriction. If the typo was in the caller's only
   capability of that kind, every request fails that check. The caller is
   told why in a mode with warnings.
4. **A typo in the prefix itself cannot be detected.** `webpa-prefix-cdir:`
   is not a configured prefix, so the capability is ignored like any other
   service's.

## Reference

### Endpoint

```text
webpa-prefix-api:{endpoint}:{method}
```

`{endpoint}` is a regular expression. It is matched against the request
path with the `/api/vN` prefix removed, so `/api/v3/device/mac:1122/config`
is matched as `/device/mac:1122/config`. `{method}` is a lowercase HTTP verb,
or `all` for every verb.

A token needs at least one endpoint capability that matches the request, or
the request is refused.

```text
webpa-prefix-api:device/.*/config:get      read device config
webpa-prefix-api:device/.*/config:all      read and write device config
webpa-prefix-api:hook:post                 register a webhook
```

### CIDR

```text
webpa-prefix-cidr:{network}/{bits}
```

The request must come from inside at least one listed network.

```text
webpa-prefix-cidr:96.44.169.0/24
webpa-prefix-cidr:10.0.0.0/8
webpa-prefix-cidr:2001:db8::/32
```

- IPv4 and IPv6 are both allowed. An IPv4-mapped IPv6 address such as
  `::ffff:1.2.3.4` is treated as IPv4.
- `/{bits}` is required. A single host is `/32` or `/128`. A bare address
  is malformed.

**Which address is checked.** The caller origin: the nearest hop that is not
one of the deployment's own trusted proxies.

1. If the connecting peer is not a trusted proxy, the peer is the origin.
   Forwarding headers are ignored, since anyone can set them.
2. If the peer is a trusted proxy, tr1d1um reads the `Forwarded` header
   (RFC 7239), or `X-Forwarded-For` when `Forwarded` carries no `for=`.
3. It walks the addresses from the right, skipping every trusted proxy. The
   first other address is the origin.
4. If a trusted proxy forwarded nothing, the origin is unknown, and an
   unknown origin matches no network.

The trusted proxies are operator configuration, never part of a token.

### Rate

```text
webpa-prefix-rate:{count}/{window}
```

The caller may make at most `{count}` calls per `{window}` against each
tr1d1um instance.

```text
webpa-prefix-rate:3000/1m     3000 calls per minute
webpa-prefix-rate:600/1m      600 calls per minute
webpa-prefix-rate:100/24h     100 calls per day
```

- `{count}` is a positive integer. `{window}` is a duration such as `1m`,
  `1h30m` or `24h`. Days are not a unit, so `100/1d` is malformed.
- **Windows are held between one minute and one day** by default. A window
  outside that range is rescaled to the nearer bound at the same calls per
  second: `50/1s` is held as `3000/1m`, and `100/48h` as `50/24h`. Write the
  rate at the window you mean.
- **The limit is per instance.** With N instances behind a load balancer
  the caller gets roughly N times the rate overall. To allow 200 calls per
  second across 4 instances, issue `3000/1m`.
- **The whole count may be used at once.** It then refills over the window.
  `100/24h` allows 100 calls in the first minute and none until it refills.
  Use a shorter window for smoother traffic.
- **Every rate applies.** A token with `1000/1m` and `100000/24h` allows
  neither 1001 calls in a minute nor 100001 in a day. Use several rates for
  tiered limits.
- **The limit follows the caller, not the token.** A caller, identified by
  the token's `sub`, is held to every rate on every token they have
  presented recently. A cut takes effect the first time the new token is
  used. A raise waits until the old rate has not been seen for twice its
  window. To raise sooner, stop using the old token or set an override.
- **Over the limit** the caller gets `429 Too Many Requests` with a
  `Retry-After` header.

### Parameter

```text
webpa-prefix-param:{pattern}:{operation}
```

Parameter capabilities narrow what the endpoint capabilities already allow.
Every TR-181 parameter a `/config` request gets or sets must match at least
one pattern whose operation allows the request.

```text
webpa-prefix-param:Device.DeviceInfo.**:read     read anything under DeviceInfo
webpa-prefix-param:Device.WiFi.SSID.*.SSID:all   read and write each SSID name
webpa-prefix-param:Device.**                     everything, read and write
```

They govern device requests only. A caller allowed to register a webhook
receives its events whatever its parameter capabilities say.

#### Patterns

Matching ignores case. Two wildcards exist:

| Wildcard | Matches                                      |
|----------|----------------------------------------------|
| `*`      | anything within one level, so never a `.`    |
| `**`     | anything across levels, including `.`        |

| Pattern                    | Matches                                    | Does not match                      |
|----------------------------|--------------------------------------------|-------------------------------------|
| `Device.**`                | every parameter                            |                                     |
| `Device.*`                 | `Device.Foo`                               | `Device.Foo.Bar`                    |
| `Device.WiFi.SSID.*.SSID`  | `Device.WiFi.SSID.1.SSID`                  | `Device.WiFi.SSID.1.2.SSID`         |
| `Device.Foo.**.Bar`        | `Device.Foo.23.Bar`, `Device.Foo.1.2.Bar`  | `Device.Foo.Bar`                    |

Three mistakes are easy to make:

- "Everything" is `Device.**`. `Device.*` is one level only.
- `**` between two dots needs at least one level. `Device.Foo.**.Bar` does
  not match `Device.Foo.Bar`. Add a second capability if you need both.
- A wildcard can match part of a name. `Device.WiFi**` also matches
  `Device.WiFiSecrets.Key`. Keep the dot: `Device.WiFi.**`.

#### Operations

| Suffix          | Allows                                                                   |
|-----------------|--------------------------------------------------------------------------|
| none, or `:all` | both                                                                     |
| `:read`         | getting values and attributes                                            |
| `:write`        | setting values and attributes; adding, replacing and deleting table rows |

A capability without a suffix allows reading and writing. Any other suffix,
such as `:wirte`, makes the capability malformed.

#### Partial paths

A name ending in `.`, such as `Device.WiFi.`, asks for everything beneath
it. It is allowed only by a pattern that ends in `**` and matches the
partial path itself:

- `Device.WiFi.**` and `Device.**` cover `Device.WiFi.`.
- `Device.WiFi.SSID.*.**` covers `Device.WiFi.SSID.1.`.
- `Device.WiFi.*` covers nothing below one level, so it does not cover
  `Device.WiFi.`.

Table operations, meaning `POST`, `PUT` and `DELETE` on `/config/{table}`,
act on their table or row as a partial path. Deleting
`Device.NAT.PortMapping.3.` needs `:write` or `:all` on a pattern such as
`Device.NAT.PortMapping.**`.

## For operators

### Modes and rolling a kind out

Each narrowing kind has a `mode`. Every mode logs and counts what it finds.
The modes differ in whether anything is blocked and what the caller is
told.

| `mode`                   | Rejects | Tells the caller                                      |
|--------------------------|---------|-------------------------------------------------------|
| `permissive` (default)   | no      | nothing                                               |
| `permissiveWithWarnings` | no      | malformed capabilities, and what `enforce` would reject |
| `enforce`                | yes     | malformed capabilities                                |

CIDR also has `required`, which is `enforce` plus rejecting any token with
no CIDR capability. Its trial runs are `permissiveRequired` and
`permissiveRequiredWithWarnings`. Rate has no required mode; set `defaults`
instead.

A newly configured kind therefore starts as a trial run. The intended
sequence:

1. **Configure the kind with its prefix and leave `mode` unset.** Nothing
   changes for callers. Watch the `auth_capability_warning` metric and the
   `capability warning` log lines for a few days. Every hit is a request
   that `enforce` would have refused.
2. **Fix what you find.** A `malformed` warning means an issuer typo. A
   `would-reject` warning means a caller whose token does not match how
   they actually call: wrong network, too fast, or a parameter outside
   their grant. Fix the token, or add an override for that caller.
3. **Switch to `permissiveWithWarnings`.** Callers now see the same
   findings in their responses and can fix their side before anything is
   blocked.
4. **Switch to `enforce`** once the metric is quiet.

### Configuration

Each kind is configured under its own key: `cidr`, `rate` and `param`, with
endpoint settings at the top level of `auth.inbound.jwt`. Every kind takes
`prefixes` and the narrowing kinds take `mode`. The remaining knobs:

| Setting              | Kind  | What it does                                                                                                                                                                             |
|----------------------|-------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `endpointBuckets`    | all   | Regular expressions that group request paths for the `endpoint` metric label. Without them the label is `no_endpoints`.                                                                 |
| `trustedProxies`     | cidr  | Networks of the hops you operate, such as load balancers. List them, or every request will appear to come from the balancer. Each entry is a network; a single host is `/32` or `/128`. |
| `defaults`           | rate  | Rates for any caller whose token carries no rate and who has no override. Each such caller gets its own allowance. Set them to hold everyone to something before issuers add rates to tokens. With none, such a caller is unlimited. |
| `minWindow`, `maxWindow` | rate | The bounds a token's window is held within. Default `1m` and `24h`.                                                                                                                 |
| `overrides`          | cidr, rate, param | Capabilities for one caller, keyed by principal, written without the prefix (for cidr, just the networks). An override replaces whatever the caller's tokens say, looser or stricter. An empty list makes the caller unrestricted on that kind; in a cidr required mode it leaves the caller with no network, so they are refused. |
| `maxAge`             | param | How long a token carrying a given parameter capability is honored. See below.                                                                                                           |

Defaults and overrides are used as written; the window bounds do not apply
to them.

The keys of `overrides` and `maxAge` are not case sensitive. The
configuration loader lowercases every map key, so `PartnerX` and `partnerx`
are the same caller.

#### Age limits

`maxAge` caps how long a token carrying a given parameter capability is
honored, measured from the token's `iat`, however long the issuer let the
token live:

```yaml
maxAge:
  "Device.**": 30m
  "Device.WiFi.Security.**:write": 15m
```

- A token older than the cap for any capability it carries is refused on
  every request, to every endpoint, whatever it asks for. The token is
  judged as a whole.
- A key without an operation suffix applies to the pattern with any suffix.
  A key with one applies to that suffix only, where a capability with no
  suffix means `all`.
- A token with no `iat` cannot carry a limited capability.
- Overrides do not exempt a token.

### Metrics and logs

Two counters, from the `auth` package:

- `auth_capability_check` counts every authorization outcome, labeled with
  `outcome` (`accepted` or `rejected`), `reason`, `clientid`, `partnerid`,
  `endpoint` and `method`. Rejection reasons by kind:

  | Kind      | `reason`                                                     |
  |-----------|--------------------------------------------------------------|
  | endpoint  | `no_capabilities_match`                                      |
  | cidr      | `no_cidr_capability`, `origin_not_allowed`, `unknown_origin` |
  | rate      | `rate_exceeded`, `rate_denied`                               |
  | parameter | `parameter_not_allowed`, `token_too_old`, `no_issued_at`     |

  `rate_denied` is a refusal at a rate of zero, which a malformed rate
  capability produces.

- `auth_capability_warning` counts every finding that did not reject,
  labeled with `kind` (`cidr`, `rate` or `param`), `reason` and `clientid`.
  The reasons are the rejection reasons above plus `malformed`. In a
  permissive mode this is where the would-be rejections show up, which is
  what a rollout watches.

Every finding is also logged at info level with the caller's
`sat_client_id` and `sat_partner_id`. A warning is logged as
`authorizer event: capability warning` with the warning text in a `warning`
field. A refusal is logged as `authorizer event: creds rejected` with the
reason in `sat_rejection_reason`, or as `rate limiter: request rejected` for
a rate.

## For callers

### Status codes

| Problem                                        | Response                                              |
|------------------------------------------------|-------------------------------------------------------|
| No endpoint capability matches                 | `403`, text body                                      |
| Required CIDR capability missing               | `403`, text body                                      |
| Caller origin not in any CIDR, or unknown      | `403`, text body                                      |
| Over the rate                                  | `429` with `Retry-After`                              |
| A parameter not covered for its operation      | `403`, header names the operation and parameter       |
| Token too old for a capability it carries      | `403`, header names the capability, limit and age     |

### The warning header

In a mode with warnings, every response, blocked or not, carries one
`X-Webpa-Capability-Warning` header per finding. Each is written
`<reason>; key=value; ...`, and a value that is not a plain token is quoted.
A `cap` is the capability exactly as the token carries it, prefix and all.

```text
malformed; kind=cidr; cap="webpa-prefix-cidr:10.0.0.0/33"
malformed; kind=rate; cap="webpa-prefix-rate:100/1d"
malformed; kind=param; cap="webpa-prefix-param:Device.**:wirte"
would-reject; kind=cidr; reason=origin-not-allowed; origin=203.0.113.5
would-reject; kind=cidr; reason=no-cidr-capability
would-reject; kind=cidr; reason=unknown-origin
would-reject; kind=rate; reason=rate-exceeded; limit="600/1m"
would-reject; kind=param; reason=parameter-not-allowed; op=read; name=Device.WiFi.Radio.1.Channel
would-reject; kind=param; reason=token-too-old; cap="webpa-prefix-param:Device.**"; maxAge=30m0s; age=2h0m0s
rejected; kind=param; reason=parameter-not-allowed; op=write; name=Device.DeviceInfo.FriendlyName
rejected; kind=param; reason=token-too-old; cap="webpa-prefix-param:Device.**"; maxAge=30m0s; age=2h0m0s
```

Which findings you see depends on the deployment's mode:

| Finding        | Shown in                                              |
|----------------|-------------------------------------------------------|
| `malformed`    | every mode with warnings, including `enforce`         |
| `would-reject` | `permissiveWithWarnings` and `permissiveRequiredWithWarnings` |
| `rejected`     | `enforce` and `required`, on parameter refusals       |

A `malformed` finding means your token has a capability tr1d1um recognized
by prefix but could not parse. It still counts as present, so it may be
locking you out of that kind entirely. Take it to your token issuer.

A `would-reject` finding means nothing was blocked yet, but it will be once
the deployment enforces. Fix it now.

## For token issuers

- Every capability starts with the deployment's configured prefix for its
  kind. Ask the operator what they are. A capability with any other prefix
  is silently ignored.
- A typo in a capability never loosens anything, but it can lock the
  client out of that kind entirely. Watch for `malformed` warnings.
- Rates are per instance. Multiply by the instance count to get the
  fleet-wide throughput.
- Windows run from `1m` to `24h`. Write `24h`, never `1d`.
- Use several rates for tiered limits, such as `600/1m` and `50000/24h`.
  All of them apply.
- A parameter pattern without a suffix allows reading and writing. Use
  `:read` for read-only grants.
- "Everything" is `Device.**`. `Device.*` is one level only.
- Do not claim a risky parameter capability a client does not need. If the
  deployment caps its age, the whole token stops working at the cap.
- Set `iat`. A token without one cannot carry an age-limited capability.

## Worked example

A partner should read device info and change SSIDs, only from their data
center, at up to 200 calls per second across a 4-instance deployment, with
their write access expiring quickly. Their token carries:

```text
webpa-prefix-api:device/.*/config:all
webpa-prefix-cidr:96.44.169.0/24
webpa-prefix-rate:3000/1m
webpa-prefix-param:Device.DeviceInfo.**:read
webpa-prefix-param:Device.WiFi.SSID.*.SSID:all
```

The deployment runs every kind in `enforce` and sets
`maxAge: {"Device.WiFi.SSID.*.SSID:all": 1h}`.

| Request from `96.44.169.7`                         | Result                                     |
|----------------------------------------------------|--------------------------------------------|
| GET `Device.DeviceInfo.ModelName`                  | allowed                                    |
| GET `Device.DeviceInfo.`                           | allowed; `Device.DeviceInfo.**` covers it  |
| SET `Device.WiFi.SSID.1.SSID`                      | allowed                                    |
| SET `Device.DeviceInfo.FriendlyName`               | `403`: DeviceInfo is read-only             |
| GET `Device.WiFi.`                                 | `403`: no pattern covers all of WiFi       |
| GET from `203.0.113.5`                             | `403`: outside the CIDR                    |
| 3001st call within one minute to one instance      | `429`                                      |
| any request with the token two hours after issue   | `403`: the SSID capability is capped at 1h |
