package cmd

import (
	"fmt"
	"io"
	"path"
	"text/template"

	"github.com/Masterminds/sprig/v3"
	"github.com/sirupsen/logrus"
	log "github.com/sirupsen/logrus"

	"github.com/gruntwork-io/terragrunt/cli"
	"github.com/gruntwork-io/terragrunt/config"
	"github.com/gruntwork-io/terragrunt/options"
	"github.com/spf13/cobra"

	"golang.org/x/sync/errgroup"

	"context"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

//setUpLogs set the log output ans the log level
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

type DependencyDirs struct {
	// Module folder Where Terragrunt should run
	SourcePath string
	// List of releative path dependencies
	Dependencies []string
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

// Set up a cache for the getDependencies function
type getDependenciesOutput struct {
	dependencies []string
	err          error
}

var getDependenciesCache = make(map[string]getDependenciesOutput)

// Parses the terragrunt config at `path` to find all modules it depends on
func getDependencies(path string, terragruntOptions *options.TerragruntOptions) ([]string, error) {
	// Check if this path has already been computed
	cachedResult, ok := getDependenciesCache[path]
	if ok {
		return cachedResult.dependencies, cachedResult.err
	}

	// if theres no terraform source and we're ignoring parent terragrunt configs
	// return nils to indicate we should skip this project
	isParent, err := isParentModule(path, terragruntOptions)
	if err != nil {
		getDependenciesCache[path] = getDependenciesOutput{nil, err}
		return nil, err
	}
	if isParent {
		getDependenciesCache[path] = getDependenciesOutput{nil, nil}
		return nil, nil
	}

	// Parse the HCL file
	decodeTypes := []config.PartialDecodeSectionType{
		config.DependencyBlock,
		config.DependenciesBlock,
		config.TerraformBlock,
	}
	parsedConfig, err := config.PartialParseConfigFile(path, terragruntOptions, nil, decodeTypes)
	if err != nil {
		getDependenciesCache[path] = getDependenciesOutput{nil, err}
		return nil, err
	}

	// Parse out locals
	locals, err := parseLocals(path, terragruntOptions, nil)
	if err != nil {
		getDependenciesCache[path] = getDependenciesOutput{nil, err}
		return nil, err
	}

	// Get deps from locals
	dependencies := []string{}
	if locals.ExtraAtlantisDependencies != nil {
		dependencies = locals.ExtraAtlantisDependencies
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
		// TODO: Make more robust. Check for bitbucket, etc.
		if !strings.Contains(*source, "git::") && !strings.Contains(*source, "github.com") {
			dependencies = append(dependencies, filepath.Join(*source, "*.tf*"))
		}
	}

	// Get deps from `extra_arguments` fields of the `Terraform` block
	if parsedConfig.Terraform != nil && parsedConfig.Terraform.ExtraArgs != nil {
		extraArgs := parsedConfig.Terraform.ExtraArgs
		for _, arg := range extraArgs {
			if arg.RequiredVarFiles != nil {
				for _, file := range *arg.RequiredVarFiles {
					dependencies = append(dependencies, file)
				}
			}
			if arg.OptionalVarFiles != nil {
				for _, file := range *arg.OptionalVarFiles {
					dependencies = append(dependencies, file)
				}
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
		terrOpts, err := options.NewTerragruntOptions(depPath)
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
					getDependenciesCache[path] = getDependenciesOutput{nil, err}
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

	getDependenciesCache[path] = getDependenciesOutput{cascadedDeps, nil}
	return cascadedDeps, nil
}

// Creates an AtlantisProject for a directory
func createProject(sourcePath string) (*DependencyDirs, error) {
	options, err := options.NewTerragruntOptions(sourcePath)
	log.Debug("Working at: ", sourcePath)
	if err != nil {
		return nil, err
	}
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

	// If `atlantis_skip` is true on the module, then do not produce a project for it
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
func getAllTerragruntFiles() ([]string, error) {
	options, err := options.NewTerragruntOptions(gitRoot)
	if err != nil {
		return nil, err
	}

	rawPaths, err := config.FindConfigFilesInPath(gitRoot, options)
	if err != nil {
		return nil, err
	}

	paths := make([]string, 0)
	if environment != "" {
		for _, path := range rawPaths {
			if strings.Contains(path, environment) {
				paths = append(paths, path)
			}
		}
	} else {
		paths = rawPaths
	}
	return paths, nil
}

func main(cmd *cobra.Command, args []string) error {
	// Ensure the gitRoot has a trailing slash and is an absolute path
	absoluteGitRoot, err := filepath.Abs(gitRoot)
	if err != nil {
		return err
	}
	gitRoot = absoluteGitRoot + string(filepath.Separator)

	terragruntFiles, err := getAllTerragruntFiles()
	if err != nil {
		return err
	}

	for _, terragruntPath := range terragruntFiles {
		log.Debug("Discovered ", terragruntPath)
	}

	var strSlice = make([]DependencyDirs, 0)

	lock := sync.Mutex{}
	errGroup, _ := errgroup.WithContext(context.Background())

	// Concurrently looking all dependencies
	for _, terragruntPath := range terragruntFiles {
		terragruntPath := terragruntPath // https://golang.org/doc/faq#closures_and_goroutines

		errGroup.Go(func() error {
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

		if err := errGroup.Wait(); err != nil {
			return err
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

	environmentMap["development"] = "dev"
	environmentMap["staging"] = "stg"
	environmentMap["production"] = "prod"
	varsTemplate := vars{Needs: parallel, Dirs: strSlice}
	if environment != "" {
		varsTemplate.Workload = environmentMap[environment]
	} else {
		varsTemplate.Workload = ""
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
	Short: "Creates GitLab CICD Dynamic configuration",
	Long:  `Creates GitLab CICD Dynamic configuration to be run as part of an external trigger. Use carefully`,
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
	generateCmd.PersistentFlags().StringVar(&environment, "environment", "", "Name of the environment folder within `root` directory. Default is \"\"")
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
