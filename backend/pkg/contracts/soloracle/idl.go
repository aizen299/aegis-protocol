// Package soloracle embeds the IDL of the Solana oracle program, copied from solana/idl/ by
// `make backend-idl` and checked against it in CI.
package soloracle

import _ "embed"

//go:embed aegis_oracle.json
var IDL []byte
