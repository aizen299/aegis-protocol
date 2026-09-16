// Package solvault embeds the IDL of the Solana vault program, copied from solana/idl/ by
// `make backend-idl` and checked against it in CI.
package solvault

import _ "embed"

//go:embed aegis_vault.json
var IDL []byte
