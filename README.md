# terragrunt-gitlab-cicd-config

Heavily inspired on [Terragrunt Atlantis Configuration](https://github.com/transcend-io/terragrunt-atlantis-config) - This will read a Terragrunt-like directory layout and will parse an input template which should be a valid `.gitlab-ci.yml` file.

This is very work in progress

```bash
> terragrunt-gitlab-cicd-config generate --help
Created GitLab CICD Dynamic configuration to be run as part of an external trigger. Use carefully

Usage:
  terragrunt-atlantis-config generate [flags]

Flags:
      --apply-requirements atlantis apply     Requirements that must be satisfied before atlantis apply can be run. Currently the only supported requirements are `approved` and `mergeable`. Can be overridden by locals
      --cascade-dependencies                  When true, dependencies will cascade, meaning that a module will be declared to depend not only on its dependencies, but all dependencies of its dependencies all the way down. Default is true (default true)
      --environment root                      Name of the environment folder within root directory. Default is ""
  -h, --help                                  help for generate
      --ignore-dependency-blocks dependency   When true, dependencies found in dependency blocks will be ignored
      --input string                          Path of the file where Go Template configuration will be inputted. Default is .gitlab-ci.yml
      --output string                         Path of the file where configuration will be generated. Default is not to write to file (default ".gitlab-ci.yml")
      --parallel                              Enables plans and applies to happen in parallel. Default is enabled (default true)
      --root string                           Path to the root directory of the git repo you want to build config for. Default is current dir (default "/home/msoutullo/projects/iv/terragrunt-gitlab-cicd-config")

Global Flags:
  -v, --verbosity string   Log level (debug, info, warn, error, fatal, panic (default "info")
```
<!-- TOC -->

- [terragrunt-gitlab-cicd-config](#app)
  - [Get it](#get-it)
  - [Use it](#use-it)
    - [Examples](#examples)

<!-- /TOC -->

## Get it

Using go get:

```bash
go get -u github.com/kitos9112/terragrunt-gitlab-cicd-config
```

Or [download the binary](https://github.com/kitos9112/terragrunt-gitlab-cicd-config/releases/latest) from the releases page.

```bash
# Linux
curl -L https://github.com/kitos9112/terragrunt-gitlab-cicd-config/releases/download/1.1.1/terragrunt-gitlab-cicd-config_1.1.1_linux_x86_64.tar.gz | tar xz

# OS X
curl -L https://github.com/kitos9112/terragrunt-gitlab-cicd-config/releases/download/1.1.1/terragrunt-gitlab-cicd-config_1.1.1_osx_x86_64.tar.gz | tar xz

# Windows
curl -LO https://github.com/kitos9112/terragrunt-gitlab-cicd-config/releases/download/1.1.1/terragrunt-gitlab-cicd-config_1.1.1_windows_x86_64.zip
unzip terragrunt-gitlab-cicd-config_1.1.1_windows_x86_64.zip
```

## Use it

```text

terragrunt-gitlab-cicd-config [OPTIONS] [COMMAND [ARGS...]]

Generates Atlantis Config for Terragrunt projects

Usage:
  terragrunt-atlantis-config [command]

Available Commands:
  completion  generate the autocompletion script for the specified shell
  generate    Creates GitLab CICD Dynamic configuration
  help        Help about any command
  version     Version of terragrunt-atlantis-config

Flags:
  -h, --help               help for terragrunt-atlantis-config
  -v, --verbosity string   Log level (debug, info, warn, error, fatal, panic (default "info")

Use "terragrunt-atlantis-config [command] --help" for more information about a command.
```

### Examples

WIP