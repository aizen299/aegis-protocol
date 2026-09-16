package svm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"
)

// Every read is at finalized commitment: a confirmed block can still be dropped. §2.7.
const commitment = "finalized"

type rpcClient struct {
	url  string
	http *http.Client
	id   atomic.Uint64
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message) }

func newRPC(url string, client *http.Client) *rpcClient {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &rpcClient{url: url, http: client}
}

func (c *rpcClient) call(ctx context.Context, method string, params []any, out any) error {
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": c.id.Add(1), "method": method, "params": params})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return fmt.Errorf("%s: read body: %w", method, err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: http %d", method, resp.StatusCode)
	}

	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  *rpcError       `json:"error"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("%s: decode response: %w", method, err)
	}
	if envelope.Error != nil {
		return fmt.Errorf("%s: %w", method, envelope.Error)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(envelope.Result, out); err != nil {
		return fmt.Errorf("%s: decode result: %w", method, err)
	}
	return nil
}

type signatureInfo struct {
	Signature string          `json:"signature"`
	Slot      uint64          `json:"slot"`
	Err       json.RawMessage `json:"err"`
}

type transaction struct {
	Slot      uint64 `json:"slot"`
	BlockTime *int64 `json:"blockTime"`
	Meta      *struct {
		Err               json.RawMessage `json:"err"`
		InnerInstructions []struct {
			Index        int                   `json:"index"`
			Instructions []compiledInstruction `json:"instructions"`
		} `json:"innerInstructions"`
		LoadedAddresses *struct {
			Writable []string `json:"writable"`
			Readonly []string `json:"readonly"`
		} `json:"loadedAddresses"`
	} `json:"meta"`
	Transaction struct {
		Signatures []string `json:"signatures"`
		Message    struct {
			AccountKeys []string `json:"accountKeys"`
		} `json:"message"`
	} `json:"transaction"`
}

type compiledInstruction struct {
	ProgramIDIndex int    `json:"programIdIndex"`
	Accounts       []int  `json:"accounts"`
	Data           string `json:"data"`
}

type accountInfo struct {
	Owner string   `json:"owner"`
	Data  []string `json:"data"`
}

// failed reports whether an RPC `err` field names an error. It is JSON null on success.
func failed(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}
