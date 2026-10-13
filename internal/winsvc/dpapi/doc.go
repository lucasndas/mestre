// Package dpapi keeps small secrets of the Mestre on Windows: machine-scope
// DPAPI (CryptProtectData with CRYPTPROTECT_LOCAL_MACHINE and an entropy
// label) and files whose DACL grants access to one account only, protected
// from inheritance and owned by that account (docs/plataforma/cofre.md#chave-do-cofre,
// docs/arquitetura/persistencia.md#arquivos).
//
// Machine-scope DPAPI ties a blob to this Windows: a copy does not open on
// another machine. It does not keep other local accounts out, since any
// local process may decrypt it; the file ACL does that. Hence the two go
// together: Protect before WriteFile, ReadFile before Unprotect.
//
// Everything but this comment is Windows only.
package dpapi
