package classes

import (
	"context"
	"fmt"
)

// AppState is the state of a GitHub App, with the identifiers of
// docs/github/criacao-de-apps.md#estados-de-um-app.
type AppState string

// The states. AppRequested is the one before the Grand Master prepared the
// link of creation (the CLI says `solicitado`). AppUnknown is what the
// Mestre says while the GitHub integration (M5) does not tell it.
const (
	AppRequested        AppState = "solicitado"
	AppAwaitingCreation AppState = "aguardando_criacao"
	AppAwaitingInstall  AppState = "aguardando_instalacao"
	AppExpired          AppState = "expirado"
	AppReady            AppState = "pronto"
	AppFailed           AppState = "falhou"
	AppWithError        AppState = "com_erro"
	AppUnknown          AppState = ""
)

// Label is the label of the state in the interface (panel, `class check`):
// "em criação", "pronto" or "com erro".
func (s AppState) Label() string {
	switch s {
	case AppRequested, AppAwaitingCreation, AppAwaitingInstall, AppExpired:
		return "em criação"
	case AppReady:
		return "pronto"
	case AppFailed, AppWithError:
		return "com erro"
	}
	return "não verificado (o estado dos Apps chega com o GitHub)"
}

// AppDirectory knows the Apps of GitHub (identidades e Apps, M5).
type AppDirectory interface {
	// App is the state of the App with that slug; ok is false when there is
	// none.
	App(ctx context.Context, slug string) (state AppState, ok bool, err error)
	// Requested is the state of the App a class with `app: novo` asked for;
	// ok is false when nothing was asked yet.
	Requested(ctx context.Context, class string) (state AppState, ok bool, err error)
}

// AppReport is the App line of `mestre class check`.
type AppReport struct {
	// Link is the `app` of the class as written.
	Link string
	// Inherited is true when the App is the one of another class (herdar),
	// and Origin is the class whose App it is.
	Inherited bool
	Origin    string
	State     AppState
	Label     string
	// Problem, when set, is why the App cannot be resolved.
	Problem string
}

func (a AppReport) String() string {
	s := "App " + a.Link
	if a.Inherited {
		s += " (herdado de " + a.Origin + ")"
	}
	if a.Problem != "" {
		return s + ": " + a.Problem
	}
	return s + ": " + a.Label
}

// maxInheritance bounds the chain of herdar, against a cycle.
const maxInheritance = 8

// chain follows the herdar links from c up to the class that owns the App.
// owner is that class's name, link its link (novo or an existing App).
func (l *Library) chain(ctx context.Context, c *Class, table string) (owner string, link AppLink, err error) {
	owner, link = c.Name, c.App
	seen := map[string]bool{c.Name: true}
	for link.Kind == AppInherit {
		origin := link.Class
		if seen[origin] || len(seen) > maxInheritance {
			return owner, link, fmt.Errorf("herdar:%s fecha um ciclo de classes que herdam o App umas das outras", origin)
		}
		seen[origin] = true
		rec, ok, err := l.approved(ctx, origin, table)
		if err != nil {
			return owner, link, err
		}
		if !ok {
			return owner, link, fmt.Errorf("a classe de origem %q não existe ou não tem versão aprovada visível desta mesa", origin)
		}
		oc, err := l.load(rec)
		if err != nil {
			return owner, link, err
		}
		owner, link = oc.Name, oc.App
	}
	return owner, link, nil
}

// appProblems are the rules of the App link that need the classes around:
// the class to inherit from exists and does not close a cycle, it does not
// have less right over workflows than the heir, and an App that is named
// exists.
func (l *Library) appProblems(ctx context.Context, c *Class, k ClassKey) []Problem {
	bad := func(format string, a ...any) []Problem {
		return []Problem{{ClassFile + " app", fmt.Sprintf(format, a...)}}
	}
	switch c.App.Kind {
	case AppInherit:
		if _, _, err := l.chain(ctx, c, k.Table); err != nil {
			return bad("%v", err)
		}
		rec, _, err := l.approved(ctx, c.App.Class, k.Table)
		if err != nil {
			return bad("%v", err)
		}
		oc, err := l.load(rec)
		if err != nil {
			return bad("%v", err)
		}
		if workflows(c.Permissions).rank() > workflows(oc.Permissions).rank() {
			return bad("herdar:%s é recusado: `git.workflows: %s` passa do da classe de origem (`%s`); a classe precisa de um App próprio (`novo`)",
				c.App.Class, workflows(c.Permissions), workflows(oc.Permissions))
		}
	case AppExisting:
		if l.Apps == nil {
			return nil
		}
		if _, ok, err := l.Apps.App(ctx, c.App.Slug); err != nil {
			return bad("não foi possível consultar o App %q: %v", c.App.Slug, err)
		} else if !ok {
			return bad("o App %q não existe", c.App.Slug)
		}
	}
	return nil
}

func workflows(p *Permissions) Workflows {
	if p != nil && p.Git != nil && p.Git.Workflows != nil {
		return *p.Git.Workflows
	}
	return WorkflowsNever
}

// appReport is the state of the App of a class: the one of the origin class
// when it inherits; `novo` is "em criação" until the App is ready.
func (l *Library) appReport(ctx context.Context, c *Class, table string) AppReport {
	r := AppReport{Link: c.App.String()}
	if c.App.Kind == AppInherit {
		r.Inherited = true
	}
	owner, link, err := l.chain(ctx, c, table)
	if r.Inherited {
		r.Origin = owner
	}
	if err != nil {
		r.Problem = err.Error()
		r.Label = "com erro"
		return r
	}
	r.State = AppUnknown
	switch link.Kind {
	case AppNew:
		r.State = AppRequested
		if l.Apps != nil {
			if s, ok, err := l.Apps.Requested(ctx, owner); err == nil && ok {
				r.State = s
			}
		}
	case AppExisting:
		if l.Apps != nil {
			s, ok, err := l.Apps.App(ctx, link.Slug)
			switch {
			case err != nil:
				r.Problem = err.Error()
			case !ok:
				r.Problem = fmt.Sprintf("o App %q não existe", link.Slug)
			default:
				r.State = s
			}
		}
	}
	r.Label = r.State.Label()
	return r
}
