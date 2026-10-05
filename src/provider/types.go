package provider

// SessionNode is one session in the /api/tree payload. Field order is the JSON
// order on the wire; Source must stay last.
type SessionNode struct {
	ID       string         `json:"id"`
	Parent   any            `json:"parent"`
	Title    string         `json:"title"`
	Agent    any            `json:"agent"`
	Model    any            `json:"model"`
	Dir      string         `json:"dir"`
	MaxCtx   int64          `json:"maxctx"`
	In       int64          `json:"in"`
	Out      int64          `json:"out"`
	Reason   int64          `json:"reason"`
	Cr       int64          `json:"cr"`
	Cw       int64          `json:"cw"`
	Created  int64          `json:"created"`
	Updated  int64          `json:"updated"`
	Active   bool           `json:"active"`
	Dur      int64          `json:"dur"`
	Beats    []int64        `json:"beats"`
	Children []*SessionNode `json:"children"`
	Subtree  *SubtreeSums   `json:"subtree"`
	Source   string         `json:"source"`
}

// SubtreeSums are token totals over a node and all its descendants.
type SubtreeSums struct {
	In     int64 `json:"in"`
	Out    int64 `json:"out"`
	Reason int64 `json:"reason"`
	Cr     int64 `json:"cr"`
	Cw     int64 `json:"cw"`
}

// CallRec is one AI call in the /api/calls payload.
type CallRec struct {
	Mid    string `json:"mid"`
	Time   int64  `json:"time"`
	In     int64  `json:"in"`
	Cr     int64  `json:"cr"`
	Cw     int64  `json:"cw"`
	Out    int64  `json:"out"`
	Reason int64  `json:"reason"`
	Ctx    int64  `json:"ctx"`
	Hu     bool   `json:"hu"`
}

// TriggerPartText is a text part of a /api/trigger payload.
type TriggerPartText struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
	Cut  bool   `json:"cut"`
}

// TriggerPartTool is a tool part of a /api/trigger payload.
type TriggerPartTool struct {
	Kind   string `json:"kind"`
	Tool   any    `json:"tool"`
	Status any    `json:"status"`
	Input  string `json:"input"`
	CutI   bool   `json:"cut_i"`
	Output string `json:"output"`
	CutO   bool   `json:"cut_o"`
}

// TriggerResp is the /api/trigger payload: what preceded an AI call.
type TriggerResp struct {
	Case  string `json:"case"`
	Time  any    `json:"time"`
	Parts []any  `json:"parts"`
}
