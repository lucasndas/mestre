// Package connections is who talks to whom and who reads what
// (docs/orquestracao/conexoes.md): the connections between characters,
// between characters and scrolls, and between scrolls, with the reasons that
// keep each one.
//
// Service keeps them in the Guild database (the connections and
// connection_reasons tables). The engine makes the automatic ones by itself
// (Born, from the characters Service, and Reconcile, which makes them again if
// they are missing): table master and Grand Master, the characters of a table
// and its table master, a character and who recruited it, and the ones of a
// quest (LinkQuest and EndQuest, which stage 9 drives). A connection has no
// direction and exists while it has a reason; the manual one is made by a
// master (Connect) and removed by a user (Disconnect), and the automatic ones
// cannot be removed by a person while their reason exists.
//
// What an agent can see is computed at each command (View, Visible,
// CanSeeScroll, Target); Handlers gives the engine side of `list` and
// `connect`, and Guard refuses, with not_connected, the command that names a
// character that is not connected to the caller. The scrolls and tomes arrive
// with stage 7 behind the Scrolls interface.
package connections
