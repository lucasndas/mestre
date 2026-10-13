// Package machinesheet is the machine sheet (docs/guilda/ficha-da-maquina.md):
// what each machine tells the Guild about itself, how the sheet keeps itself
// up to date, where the Citadel stores it and how the requirements of a
// character are matched against the sheets.
//
// # Pieces
//
//   - Sheet is the data model; Diff and Merge are the patch of machine.sheet.
//   - Builder composes the sheet of this machine from small probes
//     (SystemProbe, ProviderProbe, OperatorProbe, BrowserProbe, ToolsProbe,
//     TranscriptionProbe). The real system probe is per operating system
//     (system_linux.go, system_windows.go); the providers, the Operator and
//     the transcription belong to later milestones and arrive as probes.
//   - Agent is the Tower side: the full sheet for the hello, then
//     machine.sheet with only the fields that changed, on a poke and on a
//     period, all on the injected clock.
//   - Registry is the Citadel side: it checks and stores the sheet of every
//     machine in machines.sheet, hands it out with the name, the exclusion
//     and whether the machine is online (from the connection), and picks the
//     machine for a character (Match, Choose).
//   - Requirements and ParseRequires are the "requires" of bases, sheets and
//     classes and their check against a sheet.
//
// # What stays out
//
// Reading the requirements out of a base, a character sheet or a class, and
// the notice to the Owner when no machine fits, belong to the milestones of
// those documents; here they are Requirements, ParseRequires, Combine and the
// Verdict of each machine. The order of preference among the machines that
// fit and the wait for a free slot belong to the characters
// (docs/orquestracao/personagens.md#escolha-da-máquina). Running a command to
// read a version (the browser on Linux, git, ffmpeg) is a Runner that the
// wiring provides over the layers that start processes.
package machinesheet
