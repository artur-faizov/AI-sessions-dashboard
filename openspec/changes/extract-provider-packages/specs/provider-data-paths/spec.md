# Spec Delta: provider-data-paths

## Purpose

Defines capability-based data-path behavior for the API and UI: which providers may
switch their data path at runtime, how the API reports and performs switching, and how
the frontend gates the path picker — replacing provider-name special cases.

## ADDED Requirements

### Requirement: Path-switching capability reporting
`GET /api/db` SHALL report the active provider's data path, availability, autodetected
candidate paths, and a `switchable` boolean reflecting whether the active provider
supports runtime path switching. Providers without path switching SHALL report
`switchable: false` and no candidates.

#### Scenario: Switchable provider reports candidates
- **WHEN** a path-switchable provider is active and `GET /api/db` is requested
- **THEN** the response includes `switchable: true`, the provider's data path,
  availability, and its autodetected candidate paths

#### Scenario: Non-switchable provider reports no candidates
- **WHEN** a non-switchable provider is active and `GET /api/db` is requested
- **THEN** the response includes `switchable: false`, the provider's data path and
  availability, and an empty candidates list

### Requirement: Path switching is capability-gated
`POST /api/db` SHALL switch the active provider's data path only when the active
provider supports switching; otherwise it SHALL reject the request with a 400 error
explaining that switching is not supported for the active provider, changing no state.
A successful switch SHALL validate the target as a readable data source for that
provider before applying it; an invalid target SHALL be rejected with an error naming
the path and SHALL leave the previous path active.

#### Scenario: Switch rejected for non-switchable provider
- **WHEN** a non-switchable provider is active and `POST /api/db` requests a path change
- **THEN** the response is 400 with an error stating path switching is unsupported, and
  the provider's data path is unchanged

#### Scenario: Valid path switches
- **WHEN** a path-switchable provider is active and `POST /api/db` names a valid data
  file for that provider
- **THEN** the switch succeeds and subsequent tree and call reads come from the new path

#### Scenario: Invalid path rejected
- **WHEN** `POST /api/db` names a path that is not a valid data source for the active
  provider
- **THEN** the response reports the failure naming the path and the previous data path
  stays active

#### Scenario: Switching is safe under concurrent reads
- **WHEN** a path switch completes while API requests are in flight
- **THEN** in-flight requests complete without error, served entirely by either the old
  or the new data source

### Requirement: Frontend path picker follows the capability
The UI SHALL show the data-path picker only when the active provider reports
`switchable: true`, and SHALL hide it otherwise. The picker visibility SHALL NOT depend
on the provider's name.

#### Scenario: Picker hidden for non-switchable provider
- **WHEN** the active provider reports `switchable: false`
- **THEN** the path picker is not shown in the header

#### Scenario: Picker shown for switchable provider
- **WHEN** the active provider reports `switchable: true`
- **THEN** the path picker is shown, including when data is missing
