# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).



## [Unreleased]

### Added

- Add thread caching feature for Slack sink.

## [2.1.1] - 2025-07-12

### Fixed

- Fix a bug where multiple `watchReasons` would only filter for the last reason in the list due to incorrect loop variable capturing.

## [2.1.0] - 2025-07-12

### Added

- Add optional `watchReasons` configuration to filter events by reason at the Kubernetes API server, preventing dropped events during event storms.

### Fixed

- Fix various linting issues.

## [2.0.1] - 2025-07-12

### Fixed

- Fix missing Slack channel ID when adding emoji reaction.

## [2.0.0] - 2025-07-11

### Added

- Add info log when forwarding events to a receiver.
- Implement Slack threads.

### Changed

- Only fetch metadata for configured kinds.



[Unreleased]: https://github.com/giantswarm/kubernetes-event-exporter/compare/v2.1.1...HEAD
[2.1.1]: https://github.com/giantswarm/kubernetes-event-exporter/compare/v2.1.0...v2.1.1
[2.1.0]: https://github.com/giantswarm/kubernetes-event-exporter/compare/v2.0.1...v2.1.0
[2.0.1]: https://github.com/giantswarm/kubernetes-event-exporter/compare/v2.0.0...v2.0.1
[2.0.0]: https://github.com/giantswarm/kubernetes-event-exporter/releases/tag/v2.0.0
