// Package solreceiver embeds the IDL of the Solana governance receiver program, copied from
// solana/idl/ by `make backend-idl` and checked against it in CI.
package solreceiver

import _ "embed"

//go:embed aegis_governance_receiver.json
var IDL []byte
