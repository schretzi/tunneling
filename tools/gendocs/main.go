// Command gendocs regenerates docs/ from the CLI's own Cobra command tree.
// Run via `make docs` (or `go run ./tools/gendocs`) after changing any
// command's flags, usage text, or subcommand structure.
package main

import (
	"log"
	"os"

	"github.com/schretzi/tunneling/cmd"

	"github.com/spf13/cobra/doc"
)

func main() {
	const outDir = "docs"
	if err := os.MkdirAll(outDir, 0o755); err != nil { // #nosec G301 -- docs/ is committed, public repo content
		log.Fatalf("creating %s: %v", outDir, err)
	}
	if err := doc.GenMarkdownTree(cmd.Root(), outDir); err != nil {
		log.Fatalf("generating docs: %v", err)
	}
}
