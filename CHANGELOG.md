# Changelog
All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](http://keepachangelog.com/en/1.0.0/)
and this project adheres to [Semantic Versioning](http://semver.org/spec/v2.0.0.html).

## [Unreleased]
- add optional config for tr1d1um to use its own authentication tokens (basic and jwt supported) [#148](https://github.com/xmidt-org/tr1d1um/pull/148)
- remove mention of XPC team in error message [#150](https://github.com/xmidt-org/tr1d1um/pull/150)
- bump golang version [#152](https://github.com/xmidt-org/tr1d1um/pull/152)
- use scratch as docker base image instead of alpine [#152](https://github.com/xmidt-org/tr1d1um/pull/152)
- add docker automation [#152](https://github.com/xmidt-org/tr1d1um/pull/152)

## [v0.4.0]
- fix a bug in which tr1d1um was returning 500 for user error requests [#146](https://github.com/xmidt-org/tr1d1um/pull/146)
- added endpoint regex configuration for capabilityCheck metric [#147](https://github.com/xmidt-org/tr1d1um/pull/147)

## [v0.3.0]
 - add feature to disable verbose transaction logger [#145](https://github.com/xmidt-org/tr1d1um/pull/145)
 - changed WRP message source [#144](https://github.com/xmidt-org/tr1d1um/pull/144)

## [v0.2.1]
 - moving partnerIDs to tr1d1um
 - Added fix to correctly parse URL for capability checking [#142](https://github.com/xmidt-org/tr1d1um/pull/142)

## [v0.2.0]
 - bumped bascule, webpa-common, and wrp-go
 - removed temporary `/iot` endpoint 
 - updated release pipeline to use travis [#135](https://github.com/xmidt-org/tr1d1um/pull/135)
 - added configurable way to check capabilities and put results into metrics, without rejecting requests [#137](https://github.com/xmidt-org/tr1d1um/pull/137)

## [v0.1.5]
 - migrated from glide to go modules
 - bumped bascule version and removed any dependencies on webpa-common secure package 

## [v0.1.4]
Add logging of WDMP parameters

## [v0.1.2]
Switching to new build process

## [0.1.1] - 2018-04-06
### Added
- Initial creation

[Unreleased]: https://github.com/xmidt-org/tr1d1um/compare/v0.4.0...HEAD
[v0.4.0]: https://github.com/xmidt-org/tr1d1um/compare/v0.3.0...v0.4.0
[v0.3.0]: https://github.com/xmidt-org/tr1d1um/compare/v0.2.1...v0.3.0
[v0.2.1]: https://github.com/xmidt-org/tr1d1um/compare/v0.2.0...v0.2.1
[v0.2.0]: https://github.com/xmidt-org/tr1d1um/compare/v0.1.5...v0.2.0
[v0.1.5]: https://github.com/xmidt-org/tr1d1um/compare/v0.1.4...v0.1.5
[v0.1.4]: https://github.com/xmidt-org/tr1d1um/compare/v0.1.2...v0.1.4
[v0.1.2]: https://github.com/xmidt-org/tr1d1um/compare/0.1.1...v0.1.2
[0.1.1]: https://github.com/xmidt-org/tr1d1um/compare/e34399980ec8f7716633c8b8bc5d72727c79b184...0.1.1
