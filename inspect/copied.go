package inspect

import (
	"fmt"
	"log"
	"path/filepath"
	"strings"

	"github.com/hashicorp/hcl/v2/hclparse"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/terraform-config-inspect/tfconfig"
)

var rootSchema = &hcl.BodySchema{
	Blocks: []hcl.BlockHeaderSchema{
		{
			Type:       "terraform",
			LabelNames: nil,
		},
	},
}

// FindTerraformBlock finds terraform files in dir and finds first occurrence of block "terraform" to read its "backend" attributes.
// This solution will not work with partial backend configuration: https://developer.hashicorp.com/terraform/language/settings/backends/configuration#partial-configuration.
// Uses logic from function loadModule from [terraform-config-inspect]/tfconfig/load_hcl.go
//
// [terraform-config-inspect]: https://github.com/hashicorp/terraform-config-inspect/
func FindTerraformBlock(dir string) (*hcl.Block, error) {
	fs := tfconfig.NewOsFs()
	primaryPaths, diags := DirFiles(fs, dir)

	log.Printf("looking for block 'terraform' in paths: %v", primaryPaths)
	parser := hclparse.NewParser()

	var terraformBlock *hcl.Block
	for _, filename := range primaryPaths {
		var file *hcl.File
		var fileDiags hcl.Diagnostics

		b, err := fs.ReadFile(filename)
		if err != nil {
			diags = append(diags, &hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  "Failed to read file",
				Detail:   fmt.Sprintf("The configuration file %q could not be read.", filename),
			})
			continue
		}
		if strings.HasSuffix(filename, ".json") {
			file, fileDiags = parser.ParseJSON(b, filename)
		} else {
			file, fileDiags = parser.ParseHCL(b, filename)
		}
		diags = append(diags, fileDiags...)
		if file == nil {
			continue
		}

		content, _, diag := file.Body.PartialContent(rootSchema)
		if diag.HasErrors() || content == nil {
			continue
		}

		for _, block := range content.Blocks {
			if block.Type == "terraform" {
				terraformBlock = block
			}
		}
	}

	return terraformBlock, nil
}

// DirFiles lists all the files which are a part of Terraform project within the fs.
// Code is a copy of unexported function dirFiles from [terraform-config-inspect]/tfconfig/load.go
//
// [terraform-config-inspect]: https://github.com/hashicorp/terraform-config-inspect/
func DirFiles(fs tfconfig.FS, dir string) (primary []string, diags hcl.Diagnostics) { //nolint:all
	infos, err := fs.ReadDir(dir)
	if err != nil {
		diags = append(diags, &hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Failed to read module directory",
			Detail:   fmt.Sprintf("Module directory %s does not exist or cannot be read.", dir),
		})
		return
	}

	var override []string
	for _, info := range infos {
		if info.IsDir() {
			// We only care about files
			continue
		}

		name := info.Name()
		ext := fileExt(name)
		if ext == "" || isIgnoredFile(name) {
			continue
		}

		baseName := name[:len(name)-len(ext)] // strip extension
		isOverride := baseName == "override" || strings.HasSuffix(baseName, "_override")

		fullPath := filepath.Join(dir, name)
		if isOverride {
			override = append(override, fullPath)
		} else {
			primary = append(primary, fullPath)
		}
	}

	// We are assuming that any _override files will be logically named,
	// and processing the files in alphabetical order. Primaries first, then overrides.
	primary = append(primary, override...)

	return
}

// fileExt returns the Terraform configuration extension of the given
// path, or a blank string if it is not a recognized extension.
func fileExt(path string) string { //nolint:all
	if strings.HasSuffix(path, ".tf") {
		return ".tf"
	} else if strings.HasSuffix(path, ".tf.json") {
		return ".tf.json"
	} else {
		return ""
	}
}

// isIgnoredFile returns true if the given filename (which must not have a
// directory path ahead of it) should be ignored as e.g. an editor swap file.
func isIgnoredFile(name string) bool { //nolint:all
	return strings.HasPrefix(name, ".") || // Unix-like hidden files
		strings.HasSuffix(name, "~") || // vim
		strings.HasPrefix(name, "#") && strings.HasSuffix(name, "#") // emacs
}
