// Package attachments receives the files a user sends (a document, a photo, a
// voice message): the size is checked before anything is downloaded, the
// download is tried three times, the type is the one detected from the content,
// the file is kept under .mestre/anexos/ of the main clone with its SHA-256
// registered, and a voice message is converted and transcribed locally
// (docs/telegram/anexos-e-transcricao.md). An attachment is content brought by a
// user: nothing here executes it, and its text (a caption, a transcript) passes
// through the masker of secrets before it reaches an agent.
package attachments
