package architecture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// txIdentifierFields are the names a transaction identifier goes by in shared code.
var txIdentifierFields = map[string]bool{"TxHash": true, "TransactionHash": true, "TxID": true}

// TestNoSharedTypeFixesATransactionIdentifiersWidth keeps transaction identity chain-neutral. An EVM
// hash is 32 bytes and a Solana signature 64, so a fixed-width array in shared code silently makes
// the second chain unrepresentable. Chain implementations under internal/chain/<vm> are exempt: they
// are where the native width belongs. See docs/v2.0-solana-plan.md §2.5.
func TestNoSharedTypeFixesATransactionIdentifiersWidth(t *testing.T) {
	root := repoRoot(t)
	fset := token.NewFileSet()
	checked := 0

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := filepath.ToSlash(strings.TrimPrefix(path, root+string(filepath.Separator)))
		if d.IsDir() {
			if strings.HasPrefix(rel, "internal/chain/") || strings.HasPrefix(rel, "pkg/contracts") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		checked++
		ast.Inspect(file, func(n ast.Node) bool {
			field, ok := n.(*ast.Field)
			if !ok {
				return true
			}
			arr, ok := field.Type.(*ast.ArrayType)
			if !ok || arr.Len == nil {
				return true
			}
			for _, name := range field.Names {
				if txIdentifierFields[name.Name] {
					t.Errorf("%s: field %s is a fixed-width array; a transaction identifier's width is chain-specific",
						fset.Position(field.Pos()), name.Name)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if checked == 0 {
		t.Fatal("no Go files were checked")
	}
}
