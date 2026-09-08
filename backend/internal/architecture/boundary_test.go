// Package architecture holds tests that enforce structural rules the compiler cannot.
package architecture

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// evmOnly lists the packages permitted to depend on an EVM driver. Everything else must reach the
// chain through internal/chain. See docs/project-spec.md §7 constraint 2.
var evmOnly = []string{
	"internal/chain/evm",
	"pkg/contracts",
}

const evmImportPrefix = "github.com/ethereum/go-ethereum"

func TestOnlyChainEVMImportsGoEthereum(t *testing.T) {
	root := repoRoot(t)
	fset := token.NewFileSet()

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if isExempt(rel) {
			return nil
		}

		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}

		for _, imp := range file.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return err
			}
			if strings.HasPrefix(p, evmImportPrefix) {
				t.Errorf("%s imports %s\n\tOnly %v may depend on an EVM driver; everything else goes through internal/chain.",
					rel, p, evmOnly)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}

// TestPkgTypesHasNoChainDrivers keeps the shared domain types VM-neutral: an Identity is 32 bytes,
// not an EVM address.
func TestPkgTypesHasNoChainDrivers(t *testing.T) {
	root := repoRoot(t)
	fset := token.NewFileSet()

	entries, err := filepath.Glob(filepath.Join(root, "pkg", "types", "*.go"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}

	for _, path := range entries {
		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, imp := range file.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if strings.Contains(p, "go-ethereum") || strings.Contains(p, "solana") {
				t.Errorf("pkg/types/%s imports %s; shared domain types must be chain-agnostic",
					filepath.Base(path), p)
			}
		}
	}
}

func isExempt(rel string) bool {
	rel = filepath.ToSlash(rel)
	for _, allowed := range evmOnly {
		if strings.HasPrefix(rel, allowed+"/") || rel == allowed {
			return true
		}
	}
	return false
}

func repoRoot(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolve root: %v", err)
	}
	return abs
}
