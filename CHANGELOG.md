# Changelog
All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](http://keepachangelog.com/en/1.0.0/)
and this project adheres to [Semantic Versioning](http://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [v0.7.12]
- [`/api/v2/device/` 500 EOF Error #328](https://github.com/xmidt-org/tr1d1um/issues/328)
- [Remove nonstandard charset for media type JSON encodings #336](https://github.com/xmidt-org/tr1d1um/issues/336)
- [Create TransactionUUID if not provided #334](https://github.com/xmidt-org/tr1d1um/issues/334)

## [v0.7.11]
- [No Prom Metrics Being Produced #329](https://github.com/xmidt-org/tr1d1um/issues/329)

## [v0.7.10]
- Remove several unused build files and update the docker images to work.  [#325](https://github.com/xmidt-org/tr1d1um/pull/325)

## [v0.7.9]
- Patch [#320](https://github.com/xmidt-org/tr1d1um/issues/320)
- [CVE-2022-32149 (High) detected in golang.org/x/text-v0.3.7](https://github.com/xmidt-org/tr1d1um/issues/317)

## [v0.7.8]
- Add support for an alternate server https://github.com/xmidt-org/tr1d1um/pull/297

## [v0.7.7]
- Uber fx integration
  - https://github.com/xmidt-org/tr1d1um/issues/288
  - https://github.com/xmidt-org/tr1d1um/issues/291
  - hub.com/xmidt-org/tr1d1um/issues/246
- Major changes to server config, sections changed
  - `primary` => `servers.primary`
  - `alternate ` => `servers.alternate`
  - `health ` => `servers.health`
  - `pprof ` => `servers.pprof`
  - `alternate` => `servers.alternate`
  - Sections `log` and `zap` were replaced with `logging`
  - Section `touchstone ` was replaced with `prometheus` 

## [v0.7.6]
- Dependency update
  - guardrails says github.com/gorilla/websocket v1.5.0 has a high vulnerability but no vulnerabilities have been filed
  -  [github.com/gorilla/sessions v1.2.1 cwe-613 no patch available](https://ossindex.sonatype.org/vulnerability/sonatype-2021-4899)
- JWT Migration #289 
  - update to use clortho `Resolver` & `Refresher`
  - update to use clortho `metrics` & `logging`
- Update ancla client initialization
- Update Config
  - Use [uber/zap](https://github.com/uber-go/zap) for clortho logging
  - Use [xmidt-org/sallust](https://github.com/xmidt-org/sallust) for the zap config unmarshalling 
  - Update auth config for clortho
  - Update ancla config

## [v0.7.4]
- Updated v2 hook endpoint to only enforce loopback validation (when configured). [#277](https://github.com/xmidt-org/tr1d1um/pull/277)

## [v0.7.3]
- Bumped ancla to v0.3.9 to fix Duration bug in webhook registration - Duration should be an int in seconds. It will also accept strings such as "5m". [#270](https://github.com/xmidt-org/tr1d1um/pull/270)
- Updated v2 webhook registration to allow for no Duration or Until set. [#270](https://github.com/xmidt-org/tr1d1um/pull/270)

## [v0.7.2]
- Fixed v2 endpoint to allow for invalid duration or until fields.  When they are invalid, the duration of the webhook is set to the configured maximum. [#266](https://github.com/xmidt-org/tr1d1um/pull/266)

## [v0.7.1]
- Renamed common folder and reallocated util.go functions. [#235](https://github.com/xmidt-org/tr1d1um/pull/235)
- Separated main.go into main.go and primaryHandler.go. [#239](https://github.com/xmidt-org/tr1d1um/pull/239)
- Updated spec file and rpkg version macro to be able to choose when the 'v' is included in the version. [#242](https://github.com/xmidt-org/tr1d1um/pull/242)
- Added configurable support for v2 endpoints with current v3 ones from the same application. [#249](https://github.com/xmidt-org/tr1d1um/pull/249)
- Added configurable support for v2 endpoints with current v3 ones from the same application. [#249](https://github.com/xmidt-org/tr1d1um/pull/249)

## [v0.7.0]
- Bumped argus to v0.6.0, bumped ancla to v0.3.5, and changed errorEncoder to pull logger from context.[#233](https://github.com/xmidt-org/tr1d1um/pull/233)
- Updated api version in url to v3 to indicate breaking changes in response codes when an invalid auth is sent.  This change was made in an earlier release (v0.5.10). [#234](https://github.com/xmidt-org/tr1d1um/pull/234)
- Updated target URL to not have an api base hard coded onto it.  Instead, the base should be provided as a part of the configuration value. [#234](https://github.com/xmidt-org/tr1d1um/pull/234)

## [v0.6.4]
- Bumped ancla to v0.3.4:
  - Changed server log source address field. [#231](https://github.com/xmidt-org/tr1d1um/pull/231)
  - Fixes a problem with wiring together configuration for the Duration and Until webhook validations. [#232](https://github.com/xmidt-org/tr1d1um/pull/232)
  - Improves logging. [#232](https://github.com/xmidt-org/tr1d1um/pull/232)

## [v0.6.3]
- Added configuration for partnerID check. [#229](https://github.com/xmidt-org/tr1d1um/pull/229)
- Bumped ancla to v0.3.2 [#229](https://github.com/xmidt-org/tr1d1um/pull/229)

## [v0.6.2]
- Bumped ancla to fix http bug. [#228](https://github.com/xmidt-org/tr1d1um/pull/228)

## [v0.6.1]
- Fixed the webhook endpoint to return 400 instead of 500 for webhook validation. [#225](https://github.com/xmidt-org/tr1d1um/pull/225)

## [v0.6.0]
- Integrated webhook validator and added documentation and configuration for it. [#224](https://github.com/xmidt-org/tr1d1um/pull/224)
- Bump bascule version which includes a security vulnerability fix. [#223](https://github.com/xmidt-org/tr1d1um/pull/223)

## [v0.5.10]
- Keep setter and getter unexported. [#219](https://github.com/xmidt-org/tr1d1um/pull/219) 
- Prevent Authorization header from getting logged. [#218](https://github.com/xmidt-org/tr1d1um/pull/218) 
- Bumped ancla, webpa-common versions. [#222](https://github.com/xmidt-org/tr1d1um/pull/222)

## [v0.5.9]
- Add support for acquiring Themis tokens through Ancla. [#215](https://github.com/xmidt-org/tr1d1um/pull/215)

## [v0.5.8]
- Use official ancla release and include bascule updates. [#213](https://github.com/xmidt-org/tr1d1um/pull/213)


## [v0.5.7]
- Fix bug where OTEL trace context was not propagated from server to outgoing client requests [#211](https://github.com/xmidt-org/tr1d1um/pull/211)

## [v0.5.6]
- Make OpenTelemetry tracing an optional feature. [#207](https://github.com/xmidt-org/tr1d1um/pull/207)

## [v0.5.5]
- Initial OpenTelemetry integration. [#197](https://github.com/xmidt-org/tr1d1um/pull/197) thanks to @Sachin4403
- OpenTelemetry integration in webhook endpoints which was skipped in earlier PR. [#201](https://github.com/xmidt-org/tr1d1um/pull/201) thanks to @Sachin4403

## [v0.5.4]
### Changed
- Migrate to github actions, normalize analysis tools, Dockerfiles and Makefiles. [#186](https://github.com/xmidt-org/tr1d1um/pull/186)
- Bump webpa-common version with xwebhook item ID format update. [#192](https://github.com/xmidt-org/tr1d1um/pull/192)
- Update webhook logic library to xmidt-org/ancla. [#194](https://github.com/xmidt-org/tr1d1um/pull/194)

### Fixed

- Fix bug in which Tr1d1um was not capturing partnerIDs correctly due to casting error. [#182](https://github.com/xmidt-org/tr1d1um/pull/182)

### Changed
- Update buildtime format in Makefile to match RPM spec file. [#185](https://github.com/xmidt-org/tr1d1um/pull/185)

## [v0.5.3]
### Fixed
- Bug in which only mTLS was allowed as valid config for a webpa server. [#181](https://github.com/xmidt-org/tr1d1um/pull/181)

## [v0.5.2]
### Changed 
- Update Argus integration. [#175](https://github.com/xmidt-org/tr1d1um/pull/175)
- Switched SNS to argus. [#168](https://github.com/xmidt-org/tr1d1um/pull/168)
- Update references to the main branch. [#144](https://github.com/xmidt-org/talaria/pull/144) 
- Bumped bascule, webpa-common, and wrp-go versions. [#173](https://github.com/xmidt-org/tr1d1um/pull/173)

## [v0.5.1]
### Fixed
- Specify allowed methods for webhook endpoints. [#163](https://github.com/xmidt-org/tr1d1um/pull/163)
- Revert to default http mux routeNotFound handler for consistency. [#163](https://github.com/xmidt-org/tr1d1um/pull/163)
- Json content type header should only be specified in 200 OK responses for stat endpoint. [#166](https://github.com/xmidt-org/tr1d1um/pull/166)
- Add special field in spruce config yml. [#159](https://github.com/xmidt-org/tr1d1um/pull/159)

### Added
- Add docker entrypoint. [154](https://github.com/xmidt-org/tr1d1um/pull/154)

### Changed
- Register for specific OS signals. [#162](https://github.com/xmidt-org/tr1d1um/pull/162)

## [v0.5.0]
- Add optional config for tr1d1um to use its own authentication tokens (basic and jwt supported). [#148](https://github.com/xmidt-org/tr1d1um/pull/148)
- Remove mention of XPC team in error message. [#150](https://github.com/xmidt-org/tr1d1um/pull/150)
- Bump golang version. [#152](https://github.com/xmidt-org/tr1d1um/pull/152)
- Use scratch as docker base image instead of alpine. [#152](https://github.com/xmidt-org/tr1d1um/pull/152)
- Add docker automation. [#152](https://github.com/xmidt-org/tr1d1um/pull/152)

## [v0.4.0]
- Fix a bug in which tr1d1um was returning 500 for user error requests. [#146](https://github.com/xmidt-org/tr1d1um/pull/146)
- Added endpoint regex configuration for capabilityCheck metric. [#147](https://github.com/xmidt-org/tr1d1um/pull/147)

## [v0.3.0]
 - Add feature to disable verbose transaction logger. [#145](https://github.com/xmidt-org/tr1d1um/pull/145)
 - Changed WRP message source. [#144](https://github.com/xmidt-org/tr1d1um/pull/144)

## [v0.2.1]
 - Moving partnerIDs to tr1d1um.
 - Added fix to correctly parse URL for capability checking. [#142](https://github.com/xmidt-org/tr1d1um/pull/142)

## [v0.2.0]
 - Bumped bascule, webpa-common, and wrp-go.
 - Removed temporary `/iot` endpoint.
 - Updated release pipeline to use travis. [#135](https://github.com/xmidt-org/tr1d1um/pull/135)
 - Added configurable way to check capabilities and put results into metrics, without rejecting requests. [#137](https://github.com/xmidt-org/tr1d1um/pull/137)

## [v0.1.5]
 - Migrated from glide to go modules.
 - Bumped bascule version and removed any dependencies on webpa-common secure package. 

## [v0.1.4]
- Add logging of WDMP parameters.

## [v0.1.2]
- Switching to new build process.

## [0.1.1] - 2018-04-06
### Added
- Initial creation.

[Unreleased]: https://github.com/xmidt-org/tr1d1um/compare/v0.7.12...HEAD
[v0.7.12]: https://github.com/xmidt-org/tr1d1um/compare/v0.7.11...v0.7.12
[v0.7.11]: https://github.com/xmidt-org/tr1d1um/compare/v0.7.10...v0.7.11
[v0.7.10]: https://github.com/xmidt-org/tr1d1um/compare/v0.7.9...v0.7.10
[v0.7.9]: https://github.com/xmidt-org/tr1d1um/compare/v0.7.8...v0.7.9
[v0.7.8]: https://github.com/xmidt-org/tr1d1um/compare/v0.7.7...v0.7.8
[v0.7.7]: https://github.com/xmidt-org/tr1d1um/compare/v0.7.6...v0.7.7
[v0.7.6]: https://github.com/xmidt-org/tr1d1um/compare/v0.7.4...v0.7.6
[v0.7.4]: https://github.com/xmidt-org/tr1d1um/compare/v0.7.3...v0.7.4
[v0.7.3]: https://github.com/xmidt-org/tr1d1um/compare/v0.7.2...v0.7.3
[v0.7.2]: https://github.com/xmidt-org/tr1d1um/compare/v0.7.1...v0.7.2
[v0.7.1]: https://github.com/xmidt-org/tr1d1um/compare/v0.7.0...v0.7.1
[v0.7.0]: https://github.com/xmidt-org/tr1d1um/compare/v0.6.4...v0.7.0
[v0.6.4]: https://github.com/xmidt-org/tr1d1um/compare/v0.6.3...v0.6.4
[v0.6.3]: https://github.com/xmidt-org/tr1d1um/compare/v0.6.2...v0.6.3
[v0.6.2]: https://github.com/xmidt-org/tr1d1um/compare/v0.6.1...v0.6.2
[v0.6.1]: https://github.com/xmidt-org/tr1d1um/compare/v0.6.0...v0.6.1
[v0.6.0]: https://github.com/xmidt-org/tr1d1um/compare/v0.5.10...v0.6.0
[v0.5.10]: https://github.com/xmidt-org/tr1d1um/compare/v0.5.9...v0.5.10
[v0.5.9]: https://github.com/xmidt-org/tr1d1um/compare/v0.5.8...v0.5.9
[v0.5.8]: https://github.com/xmidt-org/tr1d1um/compare/v0.5.7...v0.5.8
[v0.5.7]: https://github.com/xmidt-org/tr1d1um/compare/v0.5.6...v0.5.7
[v0.5.6]: https://github.com/xmidt-org/tr1d1um/compare/v0.5.5...v0.5.6
[v0.5.5]: https://github.com/xmidt-org/tr1d1um/compare/v0.5.4...v0.5.5
[v0.5.4]: https://github.com/xmidt-org/tr1d1um/compare/v0.5.3...v0.5.4
[v0.5.3]: https://github.com/xmidt-org/tr1d1um/compare/v0.5.2...v0.5.3
[v0.5.2]: https://github.com/xmidt-org/tr1d1um/compare/v0.5.1...v0.5.2
[v0.5.1]: https://github.com/xmidt-org/tr1d1um/compare/v0.5.0...v0.5.1
[v0.5.0]: https://github.com/xmidt-org/tr1d1um/compare/v0.4.0...v0.5.0
[v0.4.0]: https://github.com/xmidt-org/tr1d1um/compare/v0.3.0...v0.4.0
[v0.3.0]: https://github.com/xmidt-org/tr1d1um/compare/v0.2.1...v0.3.0
[v0.2.1]: https://github.com/xmidt-org/tr1d1um/compare/v0.2.0...v0.2.1
[v0.2.0]: https://github.com/xmidt-org/tr1d1um/compare/v0.1.5...v0.2.0
[v0.1.5]: https://github.com/xmidt-org/tr1d1um/compare/v0.1.4...v0.1.5
[v0.1.4]: https://github.com/xmidt-org/tr1d1um/compare/v0.1.2...v0.1.4
[v0.1.2]: https://github.com/xmidt-org/tr1d1um/compare/0.1.1...v0.1.2
[0.1.1]: https://github.com/xmidt-org/tr1d1um/compare/e34399980ec8f7716633c8b8bc5d72727c79b184...0.1.1
