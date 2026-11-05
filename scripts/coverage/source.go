package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

func fileDigest(name string) (string, error) {
	data, err := os.ReadFile(name)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func sourceDigest(ctx context.Context) (string, error) {
	data, err := commandOutput(ctx, nil, "git", "ls-files", "-c", "-o", "--exclude-standard", "-z")
	if err != nil {
		return "", err
	}
	names := strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00")
	slices.Sort(names)
	h := sha256.New()
	for _, name := range slices.Compact(names) {
		if name == "" {
			continue
		}
		info, err := os.Lstat(name)
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(h, "%s\x00deleted\x00", filepath.ToSlash(name))
			continue
		}
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s\x00%o\x00", filepath.ToSlash(name), info.Mode().Type())
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(name)
			if err != nil {
				return "", err
			}
			fmt.Fprintf(h, "%d\x00%s\x00", len(target), target)
			info, err = os.Stat(name)
			if err != nil {
				return "", err
			}
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("source is not a readable regular file: %s", name)
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%d\x00", len(data))
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func packageFiles(ctx context.Context, m collection) (map[string][]string, error) {
	env := append(os.Environ(), "GOOS="+m.GOOS, "GOARCH="+m.GOARCH, "CGO_ENABLED="+strings.TrimSpace(m.CGO), "GOFLAGS=")
	data, err := commandOutput(ctx, env, "go", "list", "-json", "-tags=pcm_native", "./...")
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	result := map[string][]string{}
	for {
		var p struct {
			ImportPath, Dir   string
			GoFiles, CgoFiles []string
		}
		if err := decoder.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		for _, name := range append(p.GoFiles, p.CgoFiles...) {
			f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(p.Dir, name), nil, 0)
			if err != nil {
				return nil, err
			}
			executable := false
			ast.Inspect(f, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.FuncDecl:
					executable = executable || n.Body != nil
				case *ast.FuncLit:
					executable = true
				}
				return !executable
			})
			// Type/constant-only files have no instrumentable Go statements.
			if executable {
				result[p.ImportPath] = append(result[p.ImportPath], path.Join(p.ImportPath, name))
			}
		}
		slices.Sort(result[p.ImportPath])
	}
	return result, nil
}
