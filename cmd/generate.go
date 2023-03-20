package cmd

import (
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"text/template"

	"github.com/Masterminds/sprig/v3"
	"github.com/hashicorp/go-getter"
	"github.com/sirupsen/logrus"
	log "github.com/sirupsen/logrus"

	"github.com/gruntwork-io/terragrunt/cli"
	"github.com/gruntwork-io/terragrunt/config"
	"github.com/gruntwork-io/terragrunt/options"
	"github.com/spf13/cobra"

	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
	"golang.org/x/sync/singleflight"

	"context"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// setUpLogs set the log output ans the log level
func setUpLogs(out io.Writer, level string) error {
	logrus.SetOutput(out)
	lvl, err := logrus.ParseLevel(level)
	if err != nil {
		return err
	}
	logrus.SetLevel(lvl)
	return nil
}

// Parse env vars into a map
func getEnvs() map[string]string {
	envs := os.Environ()
	m := make(map[string]string)

	for _, env := range envs {
		results := strings.Split(env, "=")
		m[results[0]] = results[1]
	}

	return m
}

// Terragrunt imports can be relative or absolute
// This makes relative paths absolute
func makePathAbsolute(path string, parentPath string) string {
	if strings.HasPrefix(path, filepath.ToSlash(gitRoot)) {
		return path
	}

	parentDir := filepath.Dir(parentPath)
	return filepath.Join(parentDir, path)
}

var requestGroup singleflight.Group

type DependencyDirs struct {
	// Module folder Where Terragrunt should run
	SourcePath string
	// List of releative path dependencies
	Dependencies []string
}

// Set up a cache for the getDependencies function
type getDependenciesOutput struct {
	dependencies []string
	err          error
}

type GetDependenciesCache struct {
	mtx  sync.RWMutex
	data map[string]getDependenciesOutput
}

func newGetDependenciesCache() *GetDependenciesCache {
	return &GetDependenciesCache{data: map[string]getDependenciesOutput{}}
}

func (m *GetDependenciesCache) set(k string, v getDependenciesOutput) {
	m.mtx.Lock()
	defer m.mtx.Unlock()
	m.data[k] = v
}

func (m *GetDependenciesCache) get(k string) (getDependenciesOutput, bool) {
	m.mtx.RLock()
	defer m.mtx.RUnlock()
	v, ok := m.data[k]
	return v, ok
}

var getDependenciesCache = newGetDependenciesCache()

func uniqueStrings(str []string) []string {
	keys := make(map[string]bool)
	list := []string{}
	for _, entry := range str {
		if _, value := keys[entry]; !value {
			keys[entry] = true
			list = append(list, entry)
		}
	}
	return list
}

func lookupProjectHcl(m map[string][]string, value string) (key string) {
	for k, values := range m {
		for _, val := range values {
			if val == value {
				key = k
				return
			}
		}
	}
	return key
}

// sliceUnion takes two slices of strings and produces a union of them, containing only unique values
func sliceUnion(a, b []string) []string {
	m := make(map[string]bool)

	for _, item := range a {
		m[item] = true
	}

	for _, item := range b {
		if _, ok := m[item]; !ok {
			a = append(a, item)
		}
	}
	return a
}

// Parses the terragrunt config at `path` to find all modules it depends on
func getDependencies(path string, terragruntOptions *options.TerragruntOptions) ([]string, error) {
	res, err, _ := requestGroup.Do(path, func() (interface{}, error) {
		// Check if this path has already been computed
		cachedResult, ok := getDependenciesCache.get(path)
		if ok {
			return cachedResult.dependencies, cachedResult.err
		}

		// parse the module path to find what it includes, as well as its potential to be a parent
		// return nils to indicate we should skip this project
		isParent, includes, err := parseModule(path, terragruntOptions)
		if err != nil {
			getDependenciesCache.set(path, getDependenciesOutput{nil, err})
			return nil, err
		}
		if isParent {
			getDependenciesCache.set(path, getDependenciesOutput{nil, nil})
			return nil, nil
		}

		dependencies := []string{}
		if len(includes) > 0 {
			for _, includeDep := range includes {
				getDependenciesCache.set(includeDep.Path, getDependenciesOutput{nil, err})
				dependencies = append(dependencies, includeDep.Path)
			}
		}

		// Parse the HCL file
		decodeTypes := []config.PartialDecodeSectionType{
			config.DependencyBlock,
			config.DependenciesBlock,
			config.TerraformBlock,
		}
		parsedConfig, err := config.PartialParseConfigFile(path, terragruntOptions, nil, decodeTypes)
		if err != nil {
			getDependenciesCache.set(path, getDependenciesOutput{nil, err})
			return nil, err
		}

		// Parse out locals
		locals, err := parseLocals(path, terragruntOptions, nil)
		if err != nil {
			getDependenciesCache.set(path, getDependenciesOutput{nil, err})
			return nil, err
		}

		// Get deps from locals
		if locals.ExtraGitlabCiDependencies != nil {
			dependencies = sliceUnion(dependencies, locals.ExtraGitlabCiDependencies)
		}

		// Get deps from `dependencies` and `dependency` blocks
		if parsedConfig.Dependencies != nil && !ignoreDependencyBlocks {
			for _, parsedPaths := range parsedConfig.Dependencies.Paths {
				dependencies = append(dependencies, filepath.Join(parsedPaths, "terragrunt.hcl"))
			}
		}

		// Get deps from the `Source` field of the `Terraform` block
		if parsedConfig.Terraform != nil && parsedConfig.Terraform.Source != nil {
			source := parsedConfig.Terraform.Source

			// Use `go-getter` to normalize the source paths
			parsedSource, err := getter.Detect(*source, filepath.Dir(path), getter.Detectors)
			if err != nil {
				return nil, err
			}

			// Check if the path begins with a drive letter, denoting Windows
			isWindowsPath, err := regexp.MatchString(`^[A-Z]:`, parsedSource)
			if err != nil {
				return nil, err
			}

			// If the normalized source begins with `file://`, or matched the Windows drive letter check, it is a local path
			if strings.HasPrefix(parsedSource, "file://") || isWindowsPath {
				// Remove the prefix so we have a valid filesystem path
				parsedSource = strings.TrimPrefix(parsedSource, "file://")

				dependencies = append(dependencies, filepath.Join(parsedSource, "*.tf*"))

				ls, err := parseTerraformLocalModuleSource(parsedSource)
				if err != nil {
					return nil, err
				}
				sort.Strings(ls)

				dependencies = append(dependencies, ls...)
			}
		}

		// Get deps from `extra_arguments` fields of the `Terraform` block
		if parsedConfig.Terraform != nil && parsedConfig.Terraform.ExtraArgs != nil {
			extraArgs := parsedConfig.Terraform.ExtraArgs
			for _, arg := range extraArgs {
				if arg.RequiredVarFiles != nil {
					dependencies = append(dependencies, *arg.RequiredVarFiles...)
				}
				if arg.OptionalVarFiles != nil {
					dependencies = append(dependencies, *arg.OptionalVarFiles...)
				}
				if arg.Arguments != nil {
					for _, cliFlag := range *arg.Arguments {
						if strings.HasPrefix(cliFlag, "-var-file=") {
							dependencies = append(dependencies, strings.TrimPrefix(cliFlag, "-var-file="))
						}
					}
				}
			}
		}

		// Filter out and dependencies that are the empty string
		nonEmptyDeps := []string{}
		for _, dep := range dependencies {
			if dep != "" {
				childDepAbsPath := dep
				if !filepath.IsAbs(childDepAbsPath) {
					childDepAbsPath = makePathAbsolute(dep, path)
				}
				childDepAbsPath = filepath.ToSlash(childDepAbsPath)
				nonEmptyDeps = append(nonEmptyDeps, childDepAbsPath)
			}
		}

		// Recurse to find dependencies of all dependencies
		cascadedDeps := []string{}
		for _, dep := range nonEmptyDeps {
			cascadedDeps = append(cascadedDeps, dep)

			// The "cascading" feature is protected by a flag
			if !cascadeDependencies {
				continue
			}

			depPath := dep
			terrOpts, _ := options.NewTerragruntOptions(depPath)
			terrOpts.OriginalTerragruntConfigPath = terragruntOptions.OriginalTerragruntConfigPath
			childDeps, err := getDependencies(depPath, terrOpts)
			if err != nil {
				continue
			}

			for _, childDep := range childDeps {
				// If `childDep` is a relative path, it will be relative to `childDep`, as it is from the nested
				// `getDependencies` call on the top level module's dependencies. So here we update any relative
				// path to be from the top level module instead.
				childDepAbsPath := childDep
				if !filepath.IsAbs(childDep) {
					childDepAbsPath, err = filepath.Abs(filepath.Join(depPath, "..", childDep))
					if err != nil {
						getDependenciesCache.set(path, getDependenciesOutput{nil, err})
						return nil, err
					}
				}
				childDepAbsPath = filepath.ToSlash(childDepAbsPath)

				// Ensure we are not adding a duplicate dependency
				alreadyExists := false
				for _, dep := range cascadedDeps {
					if dep == childDepAbsPath {
						alreadyExists = true
						break
					}
				}
				if !alreadyExists {
					cascadedDeps = append(cascadedDeps, childDepAbsPath)
				}
			}
		}

		if filepath.Base(path) == "terragrunt.hcl" {
			dir := filepath.Dir(path)

			ls, err := parseTerraformLocalModuleSource(dir)
			if err != nil {
				return nil, err
			}
			sort.Strings(ls)

			cascadedDeps = append(cascadedDeps, ls...)
		}

		getDependenciesCache.set(path, getDependenciesOutput{cascadedDeps, err})
		return cascadedDeps, nil
	})

	if res != nil {
		return res.([]string), err
	} else {
		return nil, err
	}
}

// Creates a Project for a directory
func createProject(sourcePath string) (*DependencyDirs, error) {
	options, err := options.NewTerragruntOptions(sourcePath)
	log.Debug("Working at: ", sourcePath)
	if err != nil {
		return nil, err
	}
	options.OriginalTerragruntConfigPath = sourcePath
	options.RunTerragrunt = cli.RunTerragrunt
	options.Env = getEnvs()

	dependencies, err := getDependencies(sourcePath, options)
	if err != nil {
		return nil, err
	}

	// dependencies being nil is a sign from `getDependencies` that this project should be skipped
	if dependencies == nil {
		return nil, nil
	}

	absoluteSourceDir := filepath.Dir(sourcePath) + string(filepath.Separator)

	locals, err := parseLocals(sourcePath, options, nil)
	if err != nil {
		return nil, err
	}

	// If `gitlabci_skip` is true on the module, then do not produce a project for it
	if locals.Skip != nil && *locals.Skip {
		return nil, nil
	}

	// Clean up the relative path to the format Atlantis expects
	relativeSourceDir := strings.TrimPrefix(absoluteSourceDir, gitRoot)
	relativeSourceDir = strings.TrimSuffix(relativeSourceDir, string(filepath.Separator))
	if relativeSourceDir == "" {
		relativeSourceDir = "."
	}

	// Add local changes inside that directory where `terragrunt.hcl` lives
	terragruntDep := fmt.Sprintf("%s%s", relativeSourceDir, "/**/*")

	relativeDependencies := []string{
		terragruntDep,
	}

	// Add other dependencies based on their relative paths. We always want to output with Unix path separators
	for _, dependencyPath := range dependencies {
		absolutePath := dependencyPath
		if !filepath.IsAbs(absolutePath) {
			absolutePath = makePathAbsolute(dependencyPath, sourcePath)
		}
		log.Debug("Dealing with dependencyPath ", dependencyPath)
		relativeDependencies = append(relativeDependencies, strings.Split(absolutePath, gitRoot)[1])
	}

	project := &DependencyDirs{
		SourcePath:   relativeSourceDir,
		Dependencies: relativeDependencies,
	}

	return project, nil
}

// Finds the absolute paths of all terragrunt.hcl files
func getAllTerragruntFiles(path string) ([]string, error) {
	options, err := options.NewTerragruntOptions(path)
	if err != nil {
		return nil, err
	}

	// If filterPath is provided, override workingPath instead of gitRoot
	// We do this here because we want to keep the relative path structure of Terragrunt files
	// to root and just ignore the ConfigFiles
	workingPaths := []string{path}

	uniqueConfigFilePaths := make(map[string]bool)
	orderedConfigFilePaths := []string{}
	for _, workingPath := range workingPaths {
		paths, err := config.FindConfigFilesInPath(workingPath, options)
		if err != nil {
			return nil, err
		}
		for _, p := range paths {
			// if path not yet seen, insert once
			if !uniqueConfigFilePaths[p] {
				orderedConfigFilePaths = append(orderedConfigFilePaths, p)
				uniqueConfigFilePaths[p] = true
			}
		}
	}

	uniqueConfigFileAbsPaths := []string{}
	for _, uniquePath := range orderedConfigFilePaths {
		uniqueAbsPath, err := filepath.Abs(uniquePath)
		if err != nil {
			return nil, err
		}
		uniqueConfigFileAbsPaths = append(uniqueConfigFileAbsPaths, uniqueAbsPath)
	}

	return uniqueConfigFileAbsPaths, nil
}

func main(cmd *cobra.Command, args []string) error {
	// Ensure the gitRoot has a trailing slash and is an absolute path
	absoluteGitRoot, err := filepath.Abs(gitRoot)
	if err != nil {
		return err
	}
	gitRoot = absoluteGitRoot + string(filepath.Separator)
	workingDirs := []string{gitRoot}
	if err != nil {
		return err
	}

	var strSlice = make([]DependencyDirs, 0)

	lock := sync.Mutex{}
	ctx := context.Background()
	errGroup, _ := errgroup.WithContext(ctx)
	sem := semaphore.NewWeighted(500)

	// Concurrently looking all dependencies
	for _, workingDir := range workingDirs {
		log.Info("Working directory: ", workingDir)
		terragruntFiles, err := getAllTerragruntFiles(workingDir)
		if err != nil {
			return err
		}
		if workingDir == gitRoot {
			for _, terragruntPath := range terragruntFiles {
				terragruntPath := terragruntPath // https://golang.org/doc/faq#closures_and_goroutines

				// don't create atlantis projects already covered by project hcl file projects
				err := sem.Acquire(ctx, 1)
				if err != nil {
					return err
				}
				errGroup.Go(func() error {
					defer sem.Release(1)

					// check the terragrunt path contains the environment variable value
					exactEnvironment := fmt.Sprint("/(", environment, ")/")
					exactEnvironmentRegexp := regexp.MustCompile(exactEnvironment)
					environmentDoesNotMatchPath := !exactEnvironmentRegexp.Match([]byte(terragruntPath))
					if environmentDoesNotMatchPath {
						return nil
					}

					project, err := createProject(terragruntPath)
					if err != nil {
						return err
					}

					// if project and err are nil then skip this project
					if err == nil && project == nil {
						log.Debug("EMPTY Project at", terragruntPath)
						return nil
					}

					// Lock the list as only one goroutine should be writing to config.Projects at a time
					lock.Lock()
					defer lock.Unlock()

					log.Info("Collected dependencies for ", terragruntPath)
					strSlice = append(strSlice, *project)

					return nil
				})

			}
			if err := errGroup.Wait(); err != nil {
				return err
			}
		}
	}

	// Attempt to parse the input template
	inputTemplatePath := path.Base(inputTemplate)
	tpl, err := template.New(inputTemplatePath).Funcs(sprig.TxtFuncMap()).ParseFiles(inputTemplate)
	if err != nil {
		log.Error(err)
	}

	type vars struct {
		Needs    bool
		Dirs     []DependencyDirs
		Workload string
	}
	environmentMap := make(map[string]string)
	// Pre-populate the map with the environments we want to support as per Gitlab deployment tiers
	// https://docs.gitlab.com/ee/ci/environments/#deployment-tier-of-environments
	environmentMap["development"] = "dev"
	environmentMap["staging"] = "stg"
	environmentMap["production"] = "prod"

	varsTemplate := vars{Needs: parallel, Dirs: strSlice}
	if environment == "" {
		varsTemplate.Workload = ""
	} else if _, ok := environmentMap[environment]; !ok && preserveEnvironment {
		varsTemplate.Workload = environment
	} else {
		varsTemplate.Workload = environmentMap[environment]
	}

	outputPath, err := os.Create(outputPath)
	if err != nil {
		log.Error("create file: ", err)
	}

	err = tpl.Execute(outputPath, varsTemplate)
	if err != nil {
		panic(err)
	}

	return nil
}

var gitRoot string
var verbosity string
var environment string
var preserveEnvironment bool
var ignoreDependencyBlocks bool
var parallel bool
var inputTemplate string
var outputPath string
var preserveWorkflows bool
var cascadeDependencies bool
var defaultApplyRequirements []string

// generateCmd represents the generate command
var generateCmd = &cobra.Command{
	Use:   "generate",
	Short: "Creates GitLabCI Dynamic configuration",
	Long:  `Creates GitLabCI Dynamic configuration to be run as part of an external trigger. Use carefully`,
	RunE:  main,
}

func init() {
	rootCmd.AddCommand(generateCmd)

	pwd, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}

	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if err := setUpLogs(os.Stdout, verbosity); err != nil {
			return err
		}
		return nil
	}
	// Configure a root-level parameter for logging
	rootCmd.PersistentFlags().StringVarP(&verbosity, "verbosity", "v", logrus.InfoLevel.String(), "Log level (debug, info, warn, error, fatal, panic")

	// Setup `generate` subcmd config
	generateCmd.PersistentFlags().BoolVar(&ignoreDependencyBlocks, "ignore-dependency-blocks", false, "When true, dependencies found in `dependency` blocks will be ignored")
	generateCmd.PersistentFlags().BoolVar(&parallel, "parallel", true, "Enables plans and applies to happen in parallel. Default is enabled")
	generateCmd.PersistentFlags().BoolVar(&cascadeDependencies, "cascade-dependencies", true, "When true, dependencies will cascade, meaning that a module will be declared to depend not only on its dependencies, but all dependencies of its dependencies all the way down. Default is true")
	generateCmd.PersistentFlags().StringVar(&environment, "environment", "", "Name of the environment folder within `root` directory. It can be shorter if the value complies with Gitlab deployment tiers; `development`, `staging`, and `production`. Default is \"\"")
	generateCmd.PersistentFlags().BoolVar(&preserveEnvironment, "preserve-environment", false, "When true, environment name will be preserved. Default is false")
	generateCmd.PersistentFlags().StringSliceVar(&defaultApplyRequirements, "apply-requirements", []string{}, "Requirements that must be satisfied before `atlantis apply` can be run. Currently the only supported requirements are `approved` and `mergeable`. Can be overridden by locals")
	generateCmd.PersistentFlags().StringVar(&inputTemplate, "input", "", "Path of the file where Go Template configuration will be inputted. Default is .gitlab-ci.yml")
	generateCmd.PersistentFlags().StringVar(&outputPath, "output", ".gitlab-ci.yml", "Path of the file where configuration will be generated. Default is not to write to file")
	generateCmd.PersistentFlags().StringVar(&gitRoot, "root", pwd, "Path to the root directory of the git repo you want to build config for. Default is current dir")
}

// Runs a set of arguments, returning the output
func RunWithFlags(filename string, args []string) ([]byte, error) {
	rootCmd.SetArgs(args)
	rootCmd.Execute()

	return ioutil.ReadFile(filename)
}
