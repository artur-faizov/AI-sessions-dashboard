// Package provider defines the contract every dashboard data source implements
// and the payload types they produce. It is a leaf package: provider
// implementations (src/provider/<name>) and the dashboard shell both import it,
// and it imports neither.
package provider

// Provider is a data source behind the dashboard API (opencode sqlite DB,
// Claude Code projects dir, ...).
type Provider interface {
	// Name is the unique identifier used by --provider, PROVIDER and /api/provider.
	Name() string
	// Available returns nil when the data source is present; the error names the missing path.
	Available() error
	// DataPath is the source root (db file, projects dir) shown in logs and the UI.
	DataPath() string
	// FatalWhenMissing reports whether startup exits when Available fails
	// (false => serve anyway so the user can pick a data path or provider).
	FatalWhenMissing() bool

	BuildTree(nowMS int64) ([]*SessionNode, error)
	SessionCalls(sid string) ([]CallRec, error)
	SessionPrompt(sid string) ([]string, error)
	// CallTrigger returns nil, nil when the message is unknown.
	CallTrigger(mid string) (*TriggerResp, error)

	// PathSwitchable reports whether SetDataPath is supported at runtime.
	PathSwitchable() bool
	// SetDataPath validates path and switches to it; on error the previous path stays active.
	SetDataPath(path string) error
	// Candidates lists autodetected data paths for the picker (nil when not switchable).
	Candidates() []string
}
