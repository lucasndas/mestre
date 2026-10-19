package github

import (
	"context"
	"errors"
	"fmt"
)

// Who is the character an action is made for: the level and the class decide
// the App.
type Who struct {
	// Role is "grand_master", "table_master" or "character" (the roles of the
	// registry of characters).
	Role string
	// Class is the class of a common character.
	Class string
}

// Identity is the author of the commits of a character and the mark of its texts
// on GitHub.
type Identity struct {
	// Name and Email are the `user.name` and `user.email` of its gitconfig.
	Name, Email string
	// App is the App whose token the engine uses for the actions of the character.
	App string
	// Temporary is true while the App of the class is not ready and the actions
	// go out with the App "Mestre"; Class is then the class that is marked.
	Temporary bool
	Class     string
}

// Mark puts the temporary identity in a text for GitHub (a comment, a review, the
// text of an issue or of a PR): it starts with **[Class]**. A text made with the
// App of the class is left as it is.
func (i Identity) Mark(text string) string {
	if !i.Temporary || i.Class == "" {
		return text
	}
	return "**[" + i.Class + "]** " + text
}

// Trailers are the lines the message of a commit gets from the temporary
// identity: `Mestre-Classe: QA`, at the end, so the first line stays as the
// conventions of the project want it.
func (i Identity) Trailers() []string {
	if !i.Temporary || i.Class == "" {
		return nil
	}
	return []string{"Mestre-Classe: " + i.Class}
}

// Roster lists the characters alive of a class.
type Roster interface {
	AliveOfClass(ctx context.Context, class string) ([]string, error)
}

// IdentityWriter rewrites the identity file of a character that is alive (the
// gitconfig of its folder, which `GIT_CONFIG_GLOBAL` points to, so the running
// agent changes author without restarting).
type IdentityWriter interface {
	UpdateIdentity(ctx context.Context, characterID string, id Identity) error
}

// Identities decides the identity of each character.
type Identities struct {
	Apps *Registry
	// Roster and Writer are used when an App of a class becomes ready; nil: the
	// characters take the identity at their next start.
	Roster Roster
	Writer IdentityWriter
}

// AppReady is told that an App became ready (installed, with its bot known): the
// gitconfig of the characters alive of its class passes to the identity of the
// App, and the actions that come after go out with it. What was done stays as it
// was.
func (i Identities) AppReady(ctx context.Context, appName string) error {
	a, err := i.Apps.Get(ctx, appName)
	if err != nil {
		return err
	}
	if a.Role != RoleClass || !a.Ready() || a.BotUserID == 0 || i.Roster == nil || i.Writer == nil {
		return nil
	}
	ids, err := i.Roster.AliveOfClass(ctx, a.Class)
	if err != nil {
		return err
	}
	var first error
	for _, id := range ids {
		if err := i.Writer.UpdateIdentity(ctx, id, identityOf(a)); err != nil && first == nil {
			first = fmt.Errorf("github: the identity of %s: %w", id, err)
		}
	}
	return first
}

// For gives the identity of a character. The Grand Master uses the App
// "Grão-Mestre" and the table masters the App "Mestre"; a character with a
// class uses the App of the class, and, until it is ready (or while the id of
// its bot is not known), the App "Mestre" with the name of the class in the
// text. An App of a fixed role that is not ready is an error: nothing is done
// in the name of someone else.
func (i Identities) For(ctx context.Context, w Who) (Identity, error) {
	switch w.Role {
	case "grand_master":
		return i.fixed(ctx, RoleGrandMaster)
	case "table_master":
		return i.fixed(ctx, RoleMestre)
	}
	if w.Class != "" {
		a, err := i.Apps.ForClass(ctx, w.Class)
		switch {
		case err == nil && a.Ready() && a.BotUserID != 0:
			return identityOf(a), nil
		case err != nil && !errors.Is(err, ErrNoApp):
			return Identity{}, err
		}
		id, err := i.fixed(ctx, RoleMestre)
		if err != nil {
			return Identity{}, err
		}
		id.Temporary, id.Class = true, w.Class
		return id, nil
	}
	return i.fixed(ctx, RoleMestre)
}

func (i Identities) fixed(ctx context.Context, role string) (Identity, error) {
	a, err := i.Apps.Fixed(ctx, role)
	if err != nil {
		return Identity{}, err
	}
	if !a.Ready() || a.BotUserID == 0 {
		return Identity{}, fmt.Errorf("github: the App %q is not ready (%s)", a.Name, a.State.Label())
	}
	return identityOf(a), nil
}

func identityOf(a App) Identity {
	return Identity{Name: a.BotLogin(), Email: a.BotEmail(), App: a.Name}
}
