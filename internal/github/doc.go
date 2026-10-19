// Package github is the part of the Mestre that talks to GitHub as its Apps
// (docs/github/identidades-e-apps.md): the names of the Apps, the Apps and
// their installations kept in the Guild database, the identity every action
// leaves (the bot of the App of who did it), the installation tokens, the rule
// of the workflows and the repository of the lock. No agent sees a key or a
// token: the private key of each App lives in the vault, and the token lives in
// memory.
package github
