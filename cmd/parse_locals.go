package cmd

// Terragrunt doesn't give us an easy way to access all of the Locals from a module
// in an easy to digest way. This file is mostly just follows along how Terragrunt
// parses the `locals` blocks and evaluates their contents.

import (
	"github.com/gruntwork-io/go-commons/errors"
	"github.com/gruntwork-io/terragrunt/config"
	"github.com/gruntwork-io/terragrunt/options"
	"github.com/gruntwork-io/terragrunt/util"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/zclconf/go-cty/cty"

	"path/filepath"
)

// ResolvedLocals are the parsed result of local values this module cares about
type ResolvedLocals struct {
	// Extra dependencies that can be hardcoded in config
	ExtraGitlabCiDependencies []string

	// If set to true, the module will not be included in the output
	Skip *bool
}

// parseHcl uses the HCL2 parser to parse the given string into an HCL file body.
func parseHcl(parser *hclparse.Parser, hcl string, filename string) (file *hcl.File, err error) {
	// The HCL2 parser and especially cty conversions will panic in many types of errors, so we have to recover from
	// those panics here and convert them to normal errors
	defer func() {
		if recovered := recover(); recovered != nil {
			err = errors.WithStackTrace(config.PanicWhileParsingConfig{RecoveredValue: recovered, ConfigFile: filename})
		}
	}()

	if filepath.Ext(filename) == ".json" {
		file, parseDiagnostics := parser.ParseJSON([]byte(hcl), filename)
		if parseDiagnostics != nil && parseDiagnostics.HasErrors() {
			return nil, parseDiagnostics
		}

		return file, nil
	}

	file, parseDiagnostics := parser.ParseHCL([]byte(hcl), filename)
	if parseDiagnostics != nil && parseDiagnostics.HasErrors() {
		return nil, parseDiagnostics
	}

	return file, nil
}

// Merges in values from a child into a parent set of `local` values
func mergeResolvedLocals(parent ResolvedLocals, child ResolvedLocals) ResolvedLocals {

	if child.Skip != nil {
		parent.Skip = child.Skip
	}

	parent.ExtraGitlabCiDependencies = append(parent.ExtraGitlabCiDependencies, child.ExtraGitlabCiDependencies...)

	return parent
}

// Parses a given file, returning a map of all it's `local` values
func parseLocals(path string, terragruntOptions *options.TerragruntOptions, includeFromChild *config.IncludeConfig) (ResolvedLocals, error) {
	configString, err := util.ReadFileAsString(path)
	if err != nil {
		return ResolvedLocals{}, err
	}

	// Parse the HCL string into an AST body
	parser := hclparse.NewParser()
	file, err := parseHcl(parser, configString, path)
	if err != nil {
		return ResolvedLocals{}, err
	}

	// Decode just the Base blocks. See the function docs for DecodeBaseBlocks for more info on what base blocks are.
	localsAsCty, trackInclude, err := config.DecodeBaseBlocks(terragruntOptions, parser, file, path, includeFromChild, nil)
	if err != nil {
		return ResolvedLocals{}, err
	}

	// Recurse on the parent to merge in the locals from that file
	mergedParentLocals := ResolvedLocals{}
	if trackInclude != nil && includeFromChild == nil {
		for _, includeConfig := range trackInclude.CurrentList {
			parentLocals, _ := parseLocals(includeConfig.Path, terragruntOptions, &includeConfig)
			mergedParentLocals = mergeResolvedLocals(mergedParentLocals, parentLocals)
		}
	}
	childLocals := resolveLocals(*localsAsCty)

	return mergeResolvedLocals(mergedParentLocals, childLocals), nil
}

func resolveLocals(localsAsCty cty.Value) ResolvedLocals {
	resolved := ResolvedLocals{}

	// Return an empty set of locals if no `locals` block was present
	if localsAsCty == cty.NilVal {
		return resolved
	}
	rawLocals := localsAsCty.AsValueMap()

	// If the `gitlab_cicd_skip` or `gitlab_ci_skip` local is set to true, we should skip this module.
	skipValue, ok := rawLocals["gitlab_cicd_skip"]
	// If both `gitlab_cicd_skip` and `gitlab_ci_skip` are set, the latter takes precedence
	// skipValue, ok2 := rawLocals["gitlab_ci_skip"]
	if ok {
		hasValue := skipValue.True()
		resolved.Skip = &hasValue
	}

	extraDependenciesAsCty, ok := rawLocals["extra_atlantis_dependencies"]
	// If both `extra_atlantis_dependencies` and `extra_gitlabci_dependencies` are set, the latter takes precedence
	// extraDependenciesAsCty, ok2 = rawLocals["extra_gitlabci_dependencies"]
	if ok {
		it := extraDependenciesAsCty.ElementIterator()
		for it.Next() {
			_, val := it.Element()
			resolved.ExtraGitlabCiDependencies = append(
				resolved.ExtraGitlabCiDependencies,
				filepath.ToSlash(val.AsString()),
			)
		}
	}

	return resolved
}
