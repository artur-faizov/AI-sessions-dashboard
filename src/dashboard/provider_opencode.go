package dashboard

import (
	"fmt"
)

// opencodeProvider reads the switchable opencode sqlite DB through dbx.
type opencodeProvider struct{}

func (opencodeProvider) name() string { return "opencode" }

// available reports whether the current DB opens; the error names the path.
func (opencodeProvider) available() error {
	if dbx.path != "" {
		if ok, _ := validateDB(dbx.path); ok {
			return nil
		}
		return fmt.Errorf("file not found: %s", dbx.path)
	}
	path, found := resolveDB(cliDBFlag)
	if found {
		return nil
	}
	return fmt.Errorf("file not found: %s", path)
}

func (opencodeProvider) dataPath() string       { return dbx.path }
func (opencodeProvider) fatalWhenMissing() bool { return false }

func (opencodeProvider) buildTree(nowMS int64) ([]*sessionNode, error) {
	return buildTree(nowMS)
}

func (opencodeProvider) sessionPrompt(sid string) ([]string, error) {
	_, pool := poolFor()
	if pool == nil {
		return nil, errDBNotOpen
	}
	return sessionPrompt(pool, sid)
}

func (opencodeProvider) sessionCalls(sid string) ([]callRec, error) {
	_, pool := poolFor()
	if pool == nil {
		return nil, errDBNotOpen
	}
	return sessionCalls(pool, sid)
}

func (opencodeProvider) callTrigger(mid string) (*triggerResp, error) {
	_, pool := poolFor()
	if pool == nil {
		return nil, errDBNotOpen
	}
	return callTrigger(pool, mid)
}
