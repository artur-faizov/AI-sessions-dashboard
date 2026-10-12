package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"

	"github.com/artur-faizov/token-monitor/src/provider"
)

// claudeLive is one ~/.claude/sessions/<pid>.json: the live status Claude
// Code keeps for each running interactive process.
type claudeLive struct {
	PID             int    `json:"pid"`
	SessionID       string `json:"sessionId"`
	Status          string `json:"status"`     // "busy" | "idle" | "waiting"
	WaitingFor      string `json:"waitingFor"` // dialog label while waiting ("permission prompt", ...)
	StatusUpdatedAt int64  `json:"statusUpdatedAt"`
}

// liveDir is the sessions dir next to the projects dir.
func (c *Provider) liveDir() string {
	return filepath.Join(filepath.Dir(c.root), "sessions")
}

// liveStatuses maps session id -> the newest live status of a running
// process. Files of processes that are gone are skipped where /proc tells.
func (c *Provider) liveStatuses() map[string]claudeLive {
	ents, err := os.ReadDir(c.liveDir())
	if err != nil {
		return nil
	}
	_, procErr := os.Stat("/proc/self")
	out := map[string]claudeLive{}
	for _, e := range ents {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(c.liveDir(), e.Name()))
		if err != nil {
			continue
		}
		var l claudeLive
		if json.Unmarshal(b, &l) != nil || l.SessionID == "" {
			continue
		}
		if procErr == nil && l.PID > 0 {
			if _, err := os.Stat("/proc/" + strconv.Itoa(l.PID)); err != nil {
				continue
			}
		}
		if prev, ok := out[l.SessionID]; !ok || l.StatusUpdatedAt > prev.StatusUpdatedAt {
			out[l.SessionID] = l
		}
	}
	return out
}

// liveState maps a live status onto a provider state. Claude Code reports a
// question (AskUserQuestion, plan approval) and a tool permission prompt alike
// as "waiting", so the transcript tells them apart. A dialog the human opened
// themselves (a slash command) is not the agent waiting.
func liveState(l claudeLive, f *claudeFile) string {
	switch l.Status {
	case "busy":
		return provider.StateBusy
	case "idle":
		return provider.StateIdle
	case "waiting":
		switch l.WaitingFor {
		case "dialog open":
			return provider.StateIdle
		case "input needed", "goal proposal":
			return provider.StateAsk
		}
		if f != nil && f.pendingAsk() {
			return provider.StateAsk
		}
		return provider.StatePerm
	}
	return ""
}

// pendingAsk reports whether the transcript has a question-to-the-human tool
// call (AskUserQuestion, ExitPlanMode) still without its tool_result.
func (f *claudeFile) pendingAsk() bool {
	open := map[string]bool{}
	for _, m := range f.messages {
		for _, tu := range m.toolUses {
			if tu.name == "AskUserQuestion" || tu.name == "ExitPlanMode" {
				open[tu.id] = true
			}
		}
		for _, tr := range m.toolResults {
			delete(open, tr.id)
		}
	}
	return len(open) > 0
}
