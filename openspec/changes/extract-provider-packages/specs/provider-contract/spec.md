# Spec Delta: provider-contract

## Purpose

Defines the contract data sources implement to plug into the dashboard: one shared
provider interface, per-provider package isolation, entrypoint registration, and
default-selection rules, so new data sources (opencode, Claude Code, Copilot, ...)
are added without touching existing ones.

## ADDED Requirements

### Requirement: Provider contract
The system SHALL expose a single provider contract that every data source implements.
The contract SHALL cover: the provider's name, whether its data source is present, the
root path of its data, how the shell behaves at startup when the data is missing
(exit or serve anyway), and the tree, per-session calls, session prompt, and message
trigger lookups the API serves.

#### Scenario: API served through the contract
- **WHEN** any registered provider is active and the API endpoints for tree, calls,
  prompt, or trigger are requested
- **THEN** the responses are produced by the active provider through the contract, with
  no endpoint-specific knowledge of which provider is active

#### Scenario: Provider availability check
- **WHEN** a provider's data source is missing
- **THEN** its availability check fails with an error naming the missing path, and no
  other provider's availability is affected

### Requirement: Provider package isolation
Each provider's implementation SHALL live in its own package, isolated at compile time
from other providers. The shell (HTTP server, startup, frontend assets) SHALL contain
no logic that branches on a specific provider's identity; provider-specific behavior
SHALL be expressed through the contract.

#### Scenario: Adding a new provider touches only its package and registration
- **WHEN** a new data source is added
- **THEN** it is implemented in a new package plus one entry in the provider
  registration array, with no edits to existing provider packages or provider-specific
  branches added to the shell

#### Scenario: Shell has no identity checks
- **WHEN** the shell needs provider-specific behavior such as path switching or startup
  messaging
- **THEN** it consults contract capabilities or data paths, never the provider's name

### Requirement: Provider registration and default selection
Providers SHALL be instantiated at startup and passed to the shell as an ordered array
of provider instances. The first array element SHALL be the default active provider.
An explicit provider selection (CLI flag or API request) SHALL override the default.
The system SHALL NOT auto-fallback to another provider because the default's data is
missing.

#### Scenario: Default is the first registered provider
- **WHEN** the dashboard starts with no explicit provider selection
- **THEN** the first provider in the registration array is active

#### Scenario: Explicit selection overrides the default
- **WHEN** the dashboard starts with an explicit provider selection naming a
  registered provider
- **THEN** that provider is active instead of the default

#### Scenario: Unknown selection rejected
- **WHEN** an explicit selection names a provider that is not registered
- **THEN** the selection is rejected and the default remains active

### Requirement: Startup behavior when data is missing
At startup the shell SHALL ask the active provider whether its data source is present.
For providers that declare missing data fatal, the shell SHALL exit with the
provider's error. For providers that declare it non-fatal, the shell SHALL start and
serve the UI so the user can pick a data path or provider.

#### Scenario: Fatal provider exits
- **WHEN** the default provider declares missing data fatal and its data source is
  absent at startup
- **THEN** the process exits reporting the missing path

#### Scenario: Non-fatal provider serves anyway
- **WHEN** the default provider declares missing data non-fatal and its data source is
  absent at startup
- **THEN** the server starts and the UI offers data source selection

### Requirement: Stable shared payload shapes
Session, call, and trigger payloads SHALL serialize to the same JSON shapes and field
order regardless of which provider produced them, including the session `source`
field remaining the final JSON field.

#### Scenario: Payload shape unchanged after provider extraction
- **WHEN** a session tree is served before and after the provider extraction
- **THEN** the serialized nodes are byte-identical for the same underlying data
