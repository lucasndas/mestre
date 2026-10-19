package github

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/gitx"
)

// Branch is the work of a table, ready to be sent: the repository that holds it
// (the mirror of the Citadel, which has what the worktrees of the table have),
// its branch, the branch of GitHub it goes into and the commit.
type Branch struct {
	Repo gitx.Repo
	// Name is the branch of the table, Base the ref the change is read against
	// (the branch it goes into, as the mirror has it) and BaseBranch its name on
	// GitHub.
	Name, Base, BaseBranch string
	// Head is the commit of the branch.
	Head string
	// Remote is where it goes: the URL of the repository on GitHub, no credentials.
	Remote string
}

// Branches brings the work of a table to the mirror and says where it stands
// ("o motor sincroniza os worktrees da mesa com o espelho").
type Branches interface {
	Prepare(ctx context.Context, tableKey string) (Branch, error)
}

// PushAuth gives the credentials for the push of the repository: a token of the
// App of who asks, with the permission to write contents (and workflows, when
// the change touches them).
type PushAuth interface {
	PushAuth(ctx context.Context, repo string, workflows bool) (gitx.Auth, error)
}

// Workflows says the policy of a class and, for ask, whether a user approved the
// sending of this branch at this commit.
type Workflows interface {
	Policy(ctx context.Context, class string) (classes.Workflows, error)
	// Approved says the sending of the branch at the commit was approved; when
	// not, it makes sure the request exists and gives its number.
	Approved(ctx context.Context, tableKey, branch, head string) (ok bool, request int64, err error)
}

// Criteria tells the criteria of the issues of the epic that have no proof, when a
// mission of the epic was completed.
type Criteria interface {
	Unproven(ctx context.Context, tableKey string) ([]string, error)
}

// The refusals of `pr open` and `pr update`; the CLI maps each to its code.
var (
	ErrPRExists = errors.New("github: the table already has a pull request")
	// ErrBlockOwned: the block is another class's.
	ErrBlockOwned = errors.New("github: the block belongs to another class")
	// ErrNoPushSetup: the engine has no way to send the branch.
	ErrNoPushSetup = errors.New("github: the engine can not send the branch of the table now")
)

// SecretError is a branch that adds a secret of a known format: where and what, never the value.
type SecretError struct{ Findings []SecretFinding }

func (e *SecretError) Error() string {
	parts := make([]string, 0, len(e.Findings))
	for _, f := range e.Findings {
		parts = append(parts, f.String())
	}
	return "the diff adds a secret: " + strings.Join(parts, ", ")
}

// ApprovalPendingError: the sending waits for the approval of a user.
type ApprovalPendingError struct{ Request int64 }

func (e *ApprovalPendingError) Error() string {
	return fmt.Sprintf("the sending waits for the approval of request #%d", e.Request)
}

// CriteriaError: criteria of the epic with no proof.
type CriteriaError struct{ Criteria []string }

func (e *CriteriaError) Error() string {
	return "criteria with no proof: " + strings.Join(e.Criteria, ", ")
}

// HeadChangedError: the head of the PR on GitHub is not the one expected.
type HeadChangedError struct{ Head string }

func (e *HeadChangedError) Error() string { return "the head of the PR is now " + e.Head }

// Sending is what `pr open` and `pr update` need besides the GitHub API.
type Sending struct {
	Branches  Branches
	Auth      PushAuth
	Workflows Workflows
	Criteria  Criteria
}

// scanAndCheck reads the change before it is sent: the secrets in the added
// lines, and the workflows. It says whether the change touches workflows.
func (s *PRService) scanAndCheck(ctx context.Context, tableKey, class string, b Branch) (workflows bool, err error) {
	// every commit that goes: the push sends the history, not the final diff
	lines, err := b.Repo.AddedLinesEach(ctx, b.Base, b.Head)
	if err != nil {
		return false, fmt.Errorf("the change could not be read: %w", err)
	}
	if found := ScanSecrets(lines); len(found) > 0 {
		return false, &SecretError{Findings: found}
	}
	changes, err := b.Repo.DiffNameStatus(ctx, b.Base, b.Head)
	if err != nil {
		return false, fmt.Errorf("the change could not be read: %w", err)
	}
	paths := make([]string, 0, len(changes))
	for _, c := range changes {
		paths = append(paths, c.Path)
	}
	if !TouchesWorkflows(paths) {
		return false, nil
	}
	policy := classes.WorkflowsNever
	if s.Send.Workflows != nil {
		if policy, err = s.Send.Workflows.Policy(ctx, class); err != nil {
			return true, err
		}
	}
	decision, err := CheckPush(paths, policy)
	if err != nil {
		return true, err
	}
	if decision == PushNeedsApproval {
		ok, req, err := s.Send.Workflows.Approved(ctx, tableKey, b.Name, b.Head)
		if err != nil {
			return true, err
		}
		if !ok {
			return true, &ApprovalPendingError{Request: req}
		}
	}
	return true, nil
}

// push sends the branch with the token of the App of who asks.
func (s *PRService) push(ctx context.Context, repo string, b Branch, workflows, force bool) error {
	if s.Send.Auth == nil {
		return ErrNoPushSetup
	}
	// the branch of a table is never the branch it goes into
	if strings.EqualFold(b.Name, b.BaseBranch) || strings.EqualFold(b.Name, "main") || strings.EqualFold(b.Name, "master") {
		return refuse(CodeForce, "o envio na branch principal não é feito: a mesa trabalha na sua branch")
	}
	auth, err := s.Send.Auth.PushAuth(ctx, repo, workflows)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNoPushSetup, err)
	}
	// the commit that was read is the one that goes, whatever the branch is by now
	return b.Repo.PushWithAuth(ctx, auth, b.Remote, b.Name, b.Head, force)
}

// POpen is `pr open`.
type POpen struct {
	Title, Body string
	Draft       bool
	// Class is the class of who asks (its policy of workflows), Existing the PR
	// the table already has (0: none) and Op the id of the request.
	Class    string
	Existing int
	Op       string
}

// POpened is what `pr open` answers.
type POpened struct {
	Number  int
	URL     string
	HeadSHA string
}

// Open sends the branch of the table and opens its PR: the title of the epic, the
// description with `Closes #n` for the open issues under the epic.
func (s *PRService) Open(ctx context.Context, a Actor, repo string, in POpen) (POpened, error) {
	repo, err := NormalizeRepo(repo)
	if err != nil {
		return POpened{}, refuse(CodeNotAllowed, "repositório inválido")
	}
	if a.Kind != ActorTable {
		return POpened{}, refuse(CodeNotAllowed, "o PR é da mesa")
	}
	ref, ok, err := s.Tables.Epic(ctx, a.Table)
	if err != nil {
		return POpened{}, err
	}
	if !ok {
		return POpened{}, refuse(CodeNotAllowed, "a mesa %s não tem épico registrado", a.Table)
	}
	op := in.Op
	if op == "" {
		op = NewOp()
	} else if obj, found, err := s.Objects.ByOp(ctx, op); err != nil {
		return POpened{}, err
	} else if found && obj.Kind == KindPR && obj.Table == a.Table {
		return POpened{Number: obj.Number, URL: fmt.Sprintf("https://github.com/%s/pull/%d", repo, obj.Number)}, nil // a resending
	}
	if in.Existing != 0 {
		return POpened{}, ErrPRExists
	}
	if s.Send.Branches == nil {
		return POpened{}, ErrNoPushSetup
	}
	b, err := s.Send.Branches.Prepare(ctx, a.Table)
	if err != nil {
		return POpened{}, err
	}
	if s.Send.Criteria != nil {
		if open, err := s.Send.Criteria.Unproven(ctx, a.Table); err != nil {
			return POpened{}, err
		} else if len(open) > 0 {
			return POpened{}, &CriteriaError{Criteria: open}
		}
	}
	wf, err := s.scanAndCheck(ctx, a.Table, in.Class, b)
	if err != nil {
		return POpened{}, err
	}
	if err := s.push(ctx, repo, b, wf, false); err != nil {
		return POpened{}, err
	}
	title, body := in.Title, agentText(in.Body)
	issues := IssueService{API: s.API, Objects: s.Objects, Tables: s.Tables}
	tree, err := issues.ListTree(ctx, repo, ref.Epic)
	if err != nil {
		return POpened{}, err
	}
	if title == "" && len(tree) > 0 {
		title = tree[0].Title
	}
	if closes := closesOf(tree); closes != "" {
		body = strings.TrimRight(body, "\n") + "\n\n" + closes
	}
	made, err := s.API.CreatePull(ctx, repo, title, strings.TrimLeft(body, "\n")+"\n\n"+Marker(op), b.Name, b.BaseBranch, in.Draft)
	if err != nil {
		return POpened{}, err
	}
	if err := s.Objects.Register(ctx, Object{Repo: repo, Kind: KindPR, Number: made.Number, Table: a.Table, Op: op}); err != nil {
		return POpened{}, &PartialError{Kind: "pull request", Number: made.Number, Registered: false, Err: err}
	}
	return POpened{Number: made.Number, URL: made.URL, HeadSHA: b.Head}, nil
}

// PUpdate is `pr update`.
type PUpdate struct {
	Title, Body *string
	Force       bool
	Ready       bool
	// Block, BlockText and ExpectHead are `--block <name> <text> --expect-head`.
	Block, BlockText, ExpectHead string
	Class                        string
}

// allBlocks matches every block of a body.
var allBlocks = regexp.MustCompile(`(?s)<!-- mestre:bloco=[a-z-]+ -->.*?<!-- /mestre:bloco -->`)

// closing is a keyword that makes GitHub close an issue when the PR is merged.
var closing = regexp.MustCompile(`(?i)\b(close[sd]?|fix(?:e[sd])?|resolve[sd]?)\b(\s*:?\s*)((?:[\w.-]+/[\w.-]+)?#\d+|https?://\S+)`)

// agentText is what an agent writes for a title, a description or a block: the marks
// of the engine and of the blocks are taken out (until nothing is left of them,
// since one taken out may close another), and a keyword that closes an issue turns
// into a reference: only the engine says what the merge closes.
func agentText(s string) string {
	for {
		t := StripMarkers(s)
		t = strings.NewReplacer("<!-- /mestre:", "<!-- /mestre-removed:", "<!-- mestre:", "<!-- mestre-removed:").Replace(t)
		if t == s {
			break
		}
		s = t
	}
	return closing.ReplaceAllString(s, "Refs$2$3")
}

// closesOf is the "Closes #n" of each open issue of the tree of the epic.
func closesOf(tree []IssueNode) string {
	var lines []string
	for _, n := range tree {
		if n.State == "open" {
			lines = append(lines, fmt.Sprintf("Closes #%d", n.Number))
		}
	}
	return strings.Join(lines, "\n")
}

// blockRe is the mark of a block of the body.
func blockRe(name string) *regexp.Regexp {
	n := regexp.QuoteMeta(name)
	return regexp.MustCompile(`(?s)<!-- mestre:bloco=` + n + ` -->.*?<!-- /mestre:bloco -->`)
}

// BlockNames are the blocks a body may have.
var BlockNames = []string{"status", "problema", "solucao", "criterios", "historico", "antes-depois", "risco"}

// Update sends the branch again and changes the title and the description of the
// PR of the table; or rewrites one block of the description.
func (s *PRService) Update(ctx context.Context, a Actor, repo string, n int, in PUpdate) (POpened, error) {
	repo, err := s.authorize(ctx, a, repo, n)
	if err != nil {
		return POpened{}, err
	}
	if in.Block != "" {
		return POpened{}, s.updateBlock(ctx, a, repo, n, in)
	}
	if s.Send.Branches == nil {
		return POpened{}, ErrNoPushSetup
	}
	b, err := s.Send.Branches.Prepare(ctx, a.Table)
	if err != nil {
		return POpened{}, err
	}
	if in.Force {
		if err := CheckForce(a, b.Name, b.Name, b.BaseBranch); err != nil {
			return POpened{}, err
		}
	}
	wf, err := s.scanAndCheck(ctx, a.Table, in.Class, b)
	if err != nil {
		return POpened{}, err
	}
	if err := s.push(ctx, repo, b, wf, in.Force); err != nil {
		return POpened{}, err
	}
	var title, body *string
	if in.Title != nil {
		title = in.Title
	}
	if in.Body != nil {
		// what the agent writes replaces the text; the blocks the classes made, the
		// Closes of the engine and its marks stay
		cur, err := s.API.PullData(ctx, repo, n)
		if err != nil {
			return POpened{}, err
		}
		ref, ok, err := s.Tables.Epic(ctx, a.Table)
		if err != nil || !ok {
			return POpened{}, fmt.Errorf("the epic of the table could not be read")
		}
		tree, err := (&IssueService{API: s.API, Objects: s.Objects, Tables: s.Tables}).ListTree(ctx, repo, ref.Epic)
		if err != nil {
			return POpened{}, err
		}
		nb := strings.TrimRight(agentText(*in.Body), "\n")
		for _, blk := range allBlocks.FindAllString(cur.Body, -1) {
			nb += "\n\n" + blk
		}
		if closes := closesOf(tree); closes != "" {
			nb += "\n\n" + closes
		}
		nb += strings.Join(Markers(cur.Body), "")
		body = &nb
	}
	if title != nil || body != nil {
		if err := s.API.UpdatePull(ctx, repo, n, title, body); err != nil {
			return POpened{}, err
		}
	}
	p, err := s.API.PullData(ctx, repo, n)
	if err != nil {
		return POpened{}, err
	}
	if in.Ready && p.Draft {
		if err := s.API.MarkReady(ctx, repo, p.NodeID); err != nil {
			return POpened{}, err
		}
	}
	return POpened{Number: n, URL: p.URL, HeadSHA: b.Head}, nil
}

func (s *PRService) updateBlock(ctx context.Context, a Actor, repo string, n int, in PUpdate) error {
	if !slicesContains(BlockNames, in.Block) {
		return refuse(CodeNotAllowed, "o bloco %q não existe; os blocos são %s", in.Block, strings.Join(BlockNames, ", "))
	}
	p, err := s.API.PullData(ctx, repo, n)
	if err != nil {
		return err
	}
	if in.ExpectHead == "" || p.HeadSHA != in.ExpectHead {
		return &HeadChangedError{Head: p.HeadSHA}
	}
	owner, has, err := s.blockOwner(ctx, repo, n, in.Block)
	if err != nil {
		return err
	}
	if has && owner != in.Class && !a.MestreDaMesa {
		return fmt.Errorf("%w: %s", ErrBlockOwned, owner)
	}
	text := strings.TrimSpace(agentText(in.BlockText))
	block := "<!-- mestre:bloco=" + in.Block + " -->\n" + text + "\n<!-- /mestre:bloco -->"
	body := p.Body
	if re := blockRe(in.Block); re.MatchString(body) {
		body = re.ReplaceAllLiteralString(body, block)
	} else {
		body = strings.TrimRight(body, "\n") + "\n\n" + block
	}
	if err := s.API.UpdatePull(ctx, repo, n, nil, &body); err != nil {
		return err
	}
	if !has {
		return s.claimBlock(ctx, repo, n, in.Block, in.Class)
	}
	return nil
}

func slicesContains(l []string, v string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}

func (s *PRService) blockOwner(ctx context.Context, repo string, n int, name string) (string, bool, error) {
	var class string
	err := s.Objects.DB.Reader().QueryRowContext(ctx, `SELECT class FROM pr_blocks WHERE repo = ? AND pr = ? AND name = ?`, repo, n, name).Scan(&class)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return class, err == nil, err
}

func (s *PRService) claimBlock(ctx context.Context, repo string, n int, name, class string) error {
	return s.Objects.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO pr_blocks (repo, pr, name, class) VALUES (?, ?, ?, ?) ON CONFLICT (repo, pr, name) DO NOTHING`, repo, n, name, class)
		return err
	})
}
