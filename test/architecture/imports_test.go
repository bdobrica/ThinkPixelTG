package architecture

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

var forbiddenCoreImports = []string{
	"/internal/adapters/",
	"/api/",
	"github.com/jackc/pgx",
	"github.com/modelcontextprotocol/",
}

func TestCoreDoesNotImportAdaptersOrTransportTypes(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..")
	for _, packageDir := range []string{"internal/domain", "internal/app"} {
		dir := filepath.Join(root, packageDir)
		err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}

			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, imported := range file.Imports {
				importPath, err := strconv.Unquote(imported.Path.Value)
				if err != nil {
					return err
				}
				for _, forbidden := range forbiddenCoreImports {
					if strings.Contains(importPath, forbidden) {
						t.Errorf("%s imports forbidden dependency %q", path, importPath)
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("inspect %s: %v", packageDir, err)
		}
	}
}
