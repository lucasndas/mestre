package panel

import "time"

// The payloads of the central of approvals and of the bell of the notices. tygo
// turns them into web/src/gen/approvals.ts (go generate in this package).

// ApprovalAnswer is a button of the answers a user is given for a request.
type ApprovalAnswer struct {
	// Answer is the name of the answer ("approve", "approve_for", "approve_pattern",
	// "approve_always", "delegate", "deny", "deny_instruction", "deny_always",
	// "ask_back"); the front end writes its label.
	Answer string `json:"answer"`
	// Field is the data the answer asks before it is confirmed: "duration"
	// ("approve_for"), "pattern" ("approve_pattern"), "text" ("deny_instruction",
	// "ask_back") or "scope" ("delegate", which only shows the scope of the
	// delegation); "" for an answer that asks none.
	Field string `json:"field,omitempty"`
	// Suggested is the pattern the engine suggests, which the user may edit.
	Suggested string `json:"suggested,omitempty"`
	// NeedsPIN says the answer asks the PIN of the user, after the button.
	NeedsPIN bool `json:"needs_pin"`
}

// ApprovalMember is a request that shares the question with others (similar requests).
type ApprovalMember struct {
	ID    int64  `json:"id"`
	About string `json:"about"`
}

// ApprovalScope is what a delegation made from the request covers.
type ApprovalScope struct {
	Workspace string `json:"workspace"`
	Table     string `json:"table"`
	Type      string `json:"type"`
	Days      int    `json:"days"`
}

// ApprovalClosed is how a request ended: "Já respondido por <nome>: Aprovado às 14:32".
type ApprovalClosed struct {
	// State is approved, denied, expired, cancelled or invalidated.
	State string `json:"state"`
	// By is the name of the user who decided ("" when no user did) and ByID his id.
	By     string    `json:"by,omitempty"`
	ByID   string    `json:"by_id,omitempty"`
	At     time.Time `json:"at"`
	Answer string    `json:"answer,omitempty"`
	// Self says the user of the view was the one who decided.
	Self bool `json:"self"`
}

// ApprovalItem is a request waiting for the people (or one that ended, in the answer
// of GET /api/approvals/{id}).
type ApprovalItem struct {
	ID        int64  `json:"id"`
	Workspace string `json:"workspace"`
	Table     string `json:"table"`
	// Type is the type of the request ("permissao_de_classe", "pr"…) and Risk its risk.
	Type  string `json:"type"`
	Risk  string `json:"risk"`
	State string `json:"state"`
	About string `json:"about"`
	// Text is the question as the Telegram message has it, the fields of the engine;
	// Agent is the text of the agents, which the screen shows in the block
	// "texto do agente, não verificado".
	Text  string `json:"text"`
	Agent string `json:"agent"`
	// Requester is who asked ("" for a request of the engine).
	Requester string     `json:"requester"`
	CreatedAt time.Time  `json:"created_at"`
	Deadline  *time.Time `json:"deadline,omitempty"`
	// Approvals is the quorum and Counted the approvals that stand.
	Approvals int  `json:"approvals"`
	Counted   int  `json:"counted"`
	Voted     bool `json:"voted"`
	// Answers are the buttons of this user for this request, by his level and its type.
	Answers []ApprovalAnswer `json:"answers"`
	Scope   *ApprovalScope   `json:"scope,omitempty"`
	// Members are the similar requests that share the question (this one included).
	Members []ApprovalMember `json:"members"`
	Closed  *ApprovalClosed  `json:"closed,omitempty"`
}

// BlockingStep is a step of "para destravar". Action is what the panel can do for it
// ("open_vault": register the secret, on the screen of the secrets); CanDo says the
// user of the view has the level of the step, and the button shows only then.
type BlockingStep struct {
	Text   string `json:"text"`
	Action string `json:"action,omitempty"`
	CanDo  bool   `json:"can_do"`
}

// BlockingView is a notice that a table stopped, with the whole evidence, masked.
type BlockingView struct {
	ID        int64          `json:"id"`
	Workspace string         `json:"workspace"`
	Table     string         `json:"table"`
	Motive    string         `json:"motive"`
	Stopped   string         `json:"stopped"`
	Evidence  string         `json:"evidence"`
	Steps     []BlockingStep `json:"steps"`
	Owner     string         `json:"owner"`
	// Agent is the text of the agent, for the block "texto do agente, não verificado".
	Agent     string    `json:"agent"`
	CreatedAt time.Time `json:"created_at"`
}

// ApprovalList is GET /api/approvals.
type ApprovalList struct {
	Requests []ApprovalItem `json:"requests"`
	Blocking []BlockingView `json:"blocking"`
	// Pending is how many requests wait for this user, whatever the filters.
	Pending int `json:"pending"`
}

// ApprovalCount is GET /api/approvals/count.
type ApprovalCount struct {
	Pending int `json:"pending"`
}

// ApprovalAnswerRequest is POST /api/approvals/{id}/answer. The PIN, when the
// answer asks it, goes in the header PINHeader.
type ApprovalAnswerRequest struct {
	Answer string `json:"answer"`
	// Text is the instruction of "deny_instruction" and the question of "ask_back".
	Text string `json:"text,omitempty"`
	// Pattern is the pattern of "approve_pattern" and For the time of "approve_for"
	// ("90m", "3h", "2d").
	Pattern string `json:"pattern,omitempty"`
	For     string `json:"for,omitempty"`
}

// ApprovalAnswered is the answer to an answer.
type ApprovalAnswered struct {
	// State is the state of the request after it: approved, denied or open (a
	// quorum that is not complete, a question back).
	State string `json:"state"`
}

// ApprovalDiffFile is a file of the diff of a PR.
type ApprovalDiffFile struct {
	Path         string `json:"path"`
	PreviousPath string `json:"previous_path,omitempty"`
	Status       string `json:"status"`
	Added        int    `json:"added"`
	Removed      int    `json:"removed"`
	Patch        string `json:"patch"`
	// PatchMissing says the file changed and GitHub gave no patch (it is too big).
	PatchMissing bool `json:"patch_missing"`
}

// ApprovalDetails is GET /api/approvals/{id}/details: "Ver detalhes".
type ApprovalDetails struct {
	// Text is the whole question with the exact target of the request.
	Text string `json:"text"`
	// Files are the whole diff, when the request is about a PR and GitHub answers.
	Files []ApprovalDiffFile `json:"files"`
	// DiffUnavailable says the diff could not be read now.
	DiffUnavailable bool `json:"diff_unavailable"`
}

// ApprovalHistoryItem is a request that ended, read from the audit of the flow.
type ApprovalHistoryItem struct {
	ID        int64     `json:"id"`
	Workspace string    `json:"workspace"`
	Table     string    `json:"table"`
	Type      string    `json:"type"`
	Risk      string    `json:"risk"`
	State     string    `json:"state"`
	About     string    `json:"about"`
	CreatedAt time.Time `json:"created_at"`
	ClosedAt  time.Time `json:"closed_at"`
	// LevelKind is where the chain stood when it ended: agent, mesa, grand_master or
	// people; Level is its name as the people read it.
	LevelKind string `json:"level_kind"`
	Level     string `json:"level"`
	// By is the name of who decided: a user, or the character of the chain ("" when
	// nobody did: it expired or was cancelled), and ByUser says it was a user.
	By     string `json:"by"`
	ByUser bool   `json:"by_user"`
	// ByID is the id of the user, for the filter "by".
	ByID   string `json:"by_id,omitempty"`
	Answer string `json:"answer,omitempty"`
	// Reason is the reason of the decision (the agents give one).
	Reason string `json:"reason,omitempty"`
}

// ApprovalHistory is GET /api/approvals/history.
type ApprovalHistory struct {
	Items []ApprovalHistoryItem `json:"items"`
	// Next is the id to ask for the next page with ?before= (0: no more).
	Next int64 `json:"next"`
}

// ApprovalEvent is the event "changed" of the topic "approvals:<user>": a request
// changed, ended, or got a vote. The screen reloads what it shows; the event
// carries only what an item that ended needs to say who answered.
type ApprovalEvent struct {
	Request int64           `json:"request"`
	State   string          `json:"state"`
	Closed  *ApprovalClosed `json:"closed,omitempty"`
}

// ApprovalsSnapshot is the snapshot of the topic "approvals:<user>" and the event
// "count": how many requests wait for the user.
type ApprovalsSnapshot struct {
	Pending int `json:"pending"`
}

// NoticeLink says where a notice leads.
type NoticeLink struct {
	// Kind is request, table, blocking, machine or "" for none.
	Kind string `json:"kind"`
	Ref  string `json:"ref"`
}

// NoticeView is a notice of the bell.
type NoticeView struct {
	ID        int64      `json:"id"`
	Kind      string     `json:"kind"`
	Workspace string     `json:"workspace"`
	Table     string     `json:"table"`
	Text      string     `json:"text"`
	Link      NoticeLink `json:"link"`
	CreatedAt time.Time  `json:"created_at"`
	Read      bool       `json:"read"`
}

// NoticeList is GET /api/notices.
type NoticeList struct {
	Notices []NoticeView `json:"notices"`
	Unread  int          `json:"unread"`
}

// NoticeReadRequest is POST /api/notices/read: the ids to mark as read, none for all.
type NoticeReadRequest struct {
	IDs []int64 `json:"ids"`
}

// NoticesSnapshot is the snapshot of the topic "notices:<user>" and its event
// "unread": how many notices the user did not read.
type NoticesSnapshot struct {
	Unread int `json:"unread"`
}
