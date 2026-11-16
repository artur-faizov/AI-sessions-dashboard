package provider

// SessionNode is one session in the /api/tree payload. Field order is the JSON
// order on the wire; Source must stay last. Prov is set by the dashboard shell
// when it merges every provider's tree: the provider that serves the node (its
// ID is then qualified as "<prov>:<own id>"); providers leave it empty.
type SessionNode struct {
	ID       string                  `json:"id"`
	Parent   any                     `json:"parent"`
	Title    string                  `json:"title"`
	Agent    any                     `json:"agent"`
	Model    any                     `json:"model"`
	Dir      string                  `json:"dir"`
	MaxCtx   int64                   `json:"maxctx"`
	In       int64                   `json:"in"`
	Out      int64                   `json:"out"`
	Reason   int64                   `json:"reason"`
	Cr       int64                   `json:"cr"`
	Cw       int64                   `json:"cw"`
	Created  int64                   `json:"created"`
	Updated  int64                   `json:"updated"`
	Active   bool                    `json:"active"`
	Dur      int64                   `json:"dur"`
	Beats    []Beat                  `json:"beats"`
	State    string                  `json:"state,omitempty"`
	Children []*SessionNode          `json:"children"`
	Subtree  *SubtreeSums            `json:"subtree"`
	ByModel  map[string]*SubtreeSums `json:"bymodel,omitempty"`
	Prov     string                  `json:"prov,omitempty"`
	Source   string                  `json:"source"`
}

// Beat is one AI call on the ECG: [request sent, response finished] in ms.
// End is 0 while the call is still in flight.
type Beat [2]int64

// Live states of an active session (SessionNode.State); empty when unknown.
const (
	StateBusy = "busy" // the agent is working
	StateAsk  = "ask"  // the agent asked the human a question
	StatePerm = "perm" // the agent waits for the human to approve a tool
	StateIdle = "idle" // the agent finished its turn
)

// SubtreeSums are token totals over a node and all its descendants.
type SubtreeSums struct {
	In     int64 `json:"in"`
	Out    int64 `json:"out"`
	Reason int64 `json:"reason"`
	Cr     int64 `json:"cr"`
	Cw     int64 `json:"cw"`
}

// Trig is the kind of human input that triggered an AI call: typed text, an
// answered question tool, a plan approval, or an attached image. The zero
// value means the call was not triggered by human input.
type Trig string

const (
	TrigNone     Trig = ""
	TrigText     Trig = "text"
	TrigQuestion Trig = "question"
	TrigApproval Trig = "approval"
	TrigImage    Trig = "image"
)

// CallRec is one AI call in the /api/calls payload. Ask marks a call whose
// response asks the human a question (question tool). Dur is how long the call
// took to execute in ms, 0 when unknown. End is when it finished, set only
// when Time is not already the finish time (opencode records the start).
type CallRec struct {
	Mid    string `json:"mid"`
	Time   int64  `json:"time"`
	In     int64  `json:"in"`
	Cr     int64  `json:"cr"`
	Cw     int64  `json:"cw"`
	Out    int64  `json:"out"`
	Reason int64  `json:"reason"`
	Ctx    int64  `json:"ctx"`
	Hu     Trig   `json:"hu"`
	Ask    bool   `json:"ask,omitempty"`
	Model  string `json:"model,omitempty"`
	Dur    int64  `json:"dur,omitempty"`
	End    int64  `json:"end,omitempty"`
}

// Seg kinds of time a session spent besides its own AI calls.
const (
	SegTool  = "tool"  // a tool ran
	SegHuman = "human" // waiting for the human (question tool, plan approval)
	SegSub   = "sub"   // waiting for a subagent
)

// Seg is one span of the /api/calls "segs" payload. Spans of different kinds
// may overlap; the dashboard decides which one wins. Mid is the AI call that
// issued the span (empty when unknown), Name the tool or subagent type, Sub
// the subagent's session id, Input the tool's command or input, Output its
// reply, Err marks a tool that failed, Run one still running (End is then
// the time the span was read).
type Seg struct {
	Kind   string `json:"kind"`
	Start  int64  `json:"start"`
	End    int64  `json:"end"`
	Mid    string `json:"mid,omitempty"`
	Name   string `json:"name,omitempty"`
	Sub    string `json:"sub,omitempty"`
	Input  string `json:"input,omitempty"`
	Output string `json:"output,omitempty"`
	Err    bool   `json:"err,omitempty"`
	Run    bool   `json:"run,omitempty"`
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

// TriggerResp is the /api/trigger payload: what preceded an AI call. Reply
// is what the call itself showed the human: its text parts and the questions
// it asked through a question tool.
type TriggerResp struct {
	Case  string `json:"case"`
	Time  any    `json:"time"`
	Parts []any  `json:"parts"`
	Reply []any  `json:"reply,omitempty"`
}

// How a ContextItem's tokens were obtained.
const (
	EstText     = "text"     // the text is stored; tokens are estimated from it
	EstInferred = "inferred" // nothing is stored; tokens are the rest of the call's context
)

// ContextItem is one piece of an AI call's input context. Kind is a free-form
// type name chosen by the provider ("system", "instructions", "skills",
// "user", "hidden", ...); the dashboard groups items by it. Label tells items
// of one kind apart (a file path, a hook name). Text is the stored content,
// clipped at TriggerMax.
type ContextItem struct {
	Kind   string `json:"kind"`
	Label  string `json:"label,omitempty"`
	Tokens int64  `json:"tokens"`
	Est    string `json:"est"`
	Text   string `json:"text,omitempty"`
	Cut    bool   `json:"cut,omitempty"`
}

// ContextResp is the /api/context payload: the composition of one AI call's
// input context. The item tokens add up to Ctx, the call's real context size.
type ContextResp struct {
	Mid   string        `json:"mid"`
	Ctx   int64         `json:"ctx"`
	Items []ContextItem `json:"items"`
}
